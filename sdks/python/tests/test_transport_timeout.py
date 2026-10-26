from __future__ import annotations

import io
import threading
import time
import unittest
from contextlib import redirect_stderr
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from bugbarn import client
from bugbarn.client import CheckIn, Transport


class _StallOnce:
    """Ingest that never answers its first request, then accepts every other one."""

    def __init__(self) -> None:
        self.received: list[str] = []
        self.first = threading.Event()
        self.release = threading.Event()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("content-length", 0))
                self.rfile.read(length)
                if not owner.first.is_set():
                    owner.first.set()
                    owner.release.wait(timeout=10)
                    return
                owner.received.append(self.path)
                self.send_response(202)
                self.end_headers()

            def log_message(self, *args: object) -> None:
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.release.set()
        self.server.shutdown()
        self.server.server_close()


def _wait_for(condition, seconds: float = 8.0) -> bool:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if condition():
            return True
        time.sleep(0.05)
    return False


class TransportTimeoutTests(unittest.TestCase):
    def test_check_in_is_delivered_after_an_earlier_request_timed_out(self) -> None:
        ingest = _StallOnce()
        transport = Transport(api_key="k", endpoint=ingest.url)
        try:
            transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
            self.assertTrue(_wait_for(ingest.first.is_set))

            # The stalled request times out after the 2 s read limit.
            transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))

            self.assertTrue(_wait_for(lambda: bool(ingest.received)), "nothing sent after the timeout")
            self.assertEqual(ingest.received, ["/api/v1/monitors/job/check-ins"])
            self.assertTrue(transport._worker.is_alive())
        finally:
            transport.close()
            ingest.close()


class CheckInRefusedTests(unittest.TestCase):
    def test_a_refused_check_in_is_logged_once(self) -> None:
        transport = Transport(api_key="k", endpoint="http://127.0.0.1:9", maxsize=1)
        transport._closed.set()  # no worker draining: the queue stays full
        transport._worker.join(timeout=1)
        previous = client._transport
        client._transport = transport
        try:
            transport.queue.put_nowait(CheckIn(slug="fill", body={"status": "ok"}))
            err = io.StringIO()
            with redirect_stderr(err):
                self.assertFalse(client.check_in("job", "ok", interval="1h"))
                self.assertFalse(client.check_in("job", "ok", interval="1h"))
            self.assertEqual(err.getvalue().count("check-in"), 1)
            self.assertIn("job", err.getvalue())
        finally:
            client._transport = previous


if __name__ == "__main__":
    unittest.main()
