from __future__ import annotations

import io
import unittest
from contextlib import redirect_stderr

from support import StubIngest, wait_for

from bugbarn.client import CheckIn, Transport


class _Unserializable:
    """A queued message whose body json.dumps refuses, which is a TypeError."""

    def to_payload(self) -> dict[str, object]:
        return {"value": object()}


class _MalformedOnce(StubIngest):
    """Ingest whose first answer is no HTTP at all, which http.client raises as an
    HTTPException, then accepts every other request."""

    def respond(self, handler) -> None:
        if not self.first.is_set():
            self.first.set()
            handler.wfile.write(b"not http\r\n\r\n")
            return
        self.received.append(handler.path)
        handler.send_response(202)
        handler.end_headers()


class TransportWorkerTests(unittest.TestCase):
    def test_check_in_is_delivered_after_a_malformed_answer(self) -> None:
        ingest = _MalformedOnce()
        self.addCleanup(ingest.close)
        transport = Transport(api_key="k", endpoint=ingest.url)
        self.addCleanup(transport.close, 0.2)
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            transport.submit_check_in(CheckIn(slug="first", body={"status": "ok"}))
            self.assertTrue(ingest.first.wait(timeout=8))
            transport.submit_check_in(CheckIn(slug="second", body={"status": "ok"}))
            self.assertTrue(wait_for(lambda: ingest.received))
        self.assertTrue(transport._worker.is_alive())
        self.assertIn("/second/", ingest.received[0])
        self.assertIn("check-in dropped, send failed: BadStatusLine", stderr.getvalue())
        self.assertNotIn("not http", stderr.getvalue())

    def test_event_is_delivered_after_a_body_that_cannot_be_encoded(self) -> None:
        ingest = _MalformedOnce()
        ingest.first.set()
        self.addCleanup(ingest.close)
        transport = Transport(api_key="k", endpoint=ingest.url)
        self.addCleanup(transport.close, 0.2)
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            transport.submit(_Unserializable())
            self.assertTrue(wait_for(lambda: transport.queue.unfinished_tasks == 0))
            transport.submit_check_in(CheckIn(slug="after", body={"status": "ok"}))
            self.assertTrue(wait_for(lambda: ingest.received))
        self.assertTrue(transport._worker.is_alive())
        self.assertIn("event dropped, send failed: TypeError", stderr.getvalue())
        self.assertNotIn("JSON serializable", stderr.getvalue())

    def test_worker_survives_a_subclass_with_its_own_warn_once(self) -> None:
        ingest = _MalformedOnce()
        ingest.first.set()
        self.addCleanup(ingest.close)

        class OwnWarnings(Transport):
            # The shape a service wrapper uses: one argument, and a send that raises.
            def _warn_once(self, reason: str) -> None:
                pass

            def _send(self, event) -> None:
                if isinstance(event, _Unserializable):
                    raise RuntimeError("wrapper send failed")
                super()._send(event)

        transport = OwnWarnings(api_key="k", endpoint=ingest.url)
        self.addCleanup(transport.close, 0.2)
        stderr = io.StringIO()
        with redirect_stderr(stderr):
            transport.submit(_Unserializable())
            self.assertTrue(wait_for(lambda: transport.queue.unfinished_tasks == 0))
            transport.submit_check_in(CheckIn(slug="after", body={"status": "ok"}))
            self.assertTrue(wait_for(lambda: ingest.received))
        self.assertTrue(transport._worker.is_alive())
        self.assertIn("event dropped, send failed: RuntimeError", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
