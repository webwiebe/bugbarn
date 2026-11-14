from __future__ import annotations

import io
import threading
import unittest
import urllib.error
from contextlib import redirect_stderr

from support import StubIngest, make_event, wait_for

from bugbarn import client
from bugbarn.client import CheckIn, Transport


class _StallingIngest(StubIngest):
    """Ingest that never answers its first *stalls* requests, then answers *status*."""

    def __init__(self, stalls: int = 1, status: int = 202) -> None:
        self.attempts = 0
        self.stalls = stalls
        self.status = status
        self.release = threading.Event()
        super().__init__()

    def respond(self, handler) -> None:
        self.attempts += 1
        if self.attempts <= self.stalls:
            self.first.set()
            self.release.wait(timeout=10)
            return
        self.received.append(handler.path)
        handler.send_response(self.status)
        handler.end_headers()

    def close(self) -> None:
        self.release.set()
        super().close()


def _fast_check_ins(transport: Transport, delays: tuple[float, ...] = (0.0, 0.01, 0.01)) -> Transport:
    transport.check_in_timeout = 0.2
    transport.check_in_retry_delays = delays
    return transport


class TransportTimeoutTests(unittest.TestCase):
    def test_check_in_is_delivered_after_an_earlier_request_timed_out(self) -> None:
        ingest = _StallingIngest()
        transport = _fast_check_ins(Transport(api_key="k", endpoint=ingest.url), delays=(0.0,))
        try:
            with redirect_stderr(io.StringIO()):
                transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
                self.assertTrue(wait_for(ingest.first.is_set))

                # With a single attempt the stalled request is given up after the read limit.
                transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))

                self.assertTrue(wait_for(lambda: bool(ingest.received)), "nothing sent after the timeout")
            self.assertEqual(ingest.received, ["/api/v1/monitors/job/check-ins"])
            self.assertTrue(transport._worker.is_alive())
        finally:
            transport.close()
            ingest.close()


class CheckInRetryTests(unittest.TestCase):
    def test_a_stalled_check_in_is_retried_and_stays_quiet_once_delivered(self) -> None:
        ingest = _StallingIngest(stalls=2)
        transport = _fast_check_ins(Transport(api_key="k", endpoint=ingest.url))
        try:
            err = io.StringIO()
            with redirect_stderr(err):
                transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
                self.assertTrue(transport.flush(timeout=5))
            self.assertEqual(ingest.attempts, 3)
            self.assertEqual(ingest.received, ["/api/v1/monitors/job/check-ins"])
            self.assertEqual(err.getvalue(), "")
        finally:
            transport.close()
            ingest.close()

    def test_a_check_in_that_never_gets_through_warns_after_the_last_attempt(self) -> None:
        ingest = _StallingIngest(stalls=99)
        transport = _fast_check_ins(Transport(api_key="k", endpoint=ingest.url))
        try:
            err = io.StringIO()
            with redirect_stderr(err):
                transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
                self.assertTrue(transport.flush(timeout=5))
            self.assertEqual(ingest.attempts, 3)
            self.assertEqual(err.getvalue().count("check-in dropped"), 1)
            self.assertTrue(transport._worker.is_alive())
        finally:
            transport.close()
            ingest.close()

    def test_a_check_in_the_ingest_rejects_is_not_retried(self) -> None:
        ingest = _StallingIngest(stalls=0, status=401)
        transport = _fast_check_ins(Transport(api_key="k", endpoint=ingest.url))
        try:
            err = io.StringIO()
            with redirect_stderr(err):
                transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
                self.assertTrue(transport.flush(timeout=5))
            self.assertEqual(ingest.attempts, 1)
            self.assertIn("HTTP 401", err.getvalue())
        finally:
            transport.close()
            ingest.close()

    def test_close_ends_the_wait_between_attempts(self) -> None:
        ingest = _StallingIngest(stalls=99)
        transport = _fast_check_ins(Transport(api_key="k", endpoint=ingest.url), delays=(0.0, 60.0))
        try:
            transport.submit_check_in(CheckIn(slug="job", body={"status": "ok"}))
            self.assertTrue(wait_for(ingest.first.is_set))
            self.assertFalse(transport.close(timeout=0.3))
            self.assertTrue(wait_for(lambda: not transport._worker.is_alive()))
            self.assertEqual(ingest.attempts, 1)
        finally:
            ingest.close()

    def test_an_event_is_sent_once(self) -> None:
        ingest = _StallingIngest()
        transport = Transport(api_key="k", endpoint=ingest.url)
        try:
            err = io.StringIO()
            with redirect_stderr(err):
                transport.submit(make_event())
                self.assertTrue(transport.flush(timeout=5))
            self.assertEqual(ingest.attempts, 1)
            self.assertIn("event dropped", err.getvalue())
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


class ReasonNameTests(unittest.TestCase):
    def test_an_unreachable_reason_is_named_by_type_only(self) -> None:
        refused = urllib.error.URLError(ConnectionRefusedError("no route to 10.0.0.1"))
        self.assertEqual(client._reason_name(refused), "ConnectionRefusedError")
        self.assertEqual(client._reason_name(urllib.error.URLError("bad host name")), "URLError")


if __name__ == "__main__":
    unittest.main()
