from __future__ import annotations

import io
import unittest
from contextlib import redirect_stderr

import bugbarn
from bugbarn import client
from bugbarn.client import CheckIn, Envelope, Transport


def _event() -> Envelope:
    return Envelope(
        timestamp="2026-11-07T00:00:00Z",
        severityText="ERROR",
        body="boom",
        exception_type="RuntimeError",
        exception_message="boom",
    )


def _stopped_transport(maxsize: int = 1) -> Transport:
    """A transport whose worker has ended, so the queue is never drained."""
    transport = Transport(api_key="k", endpoint="http://127.0.0.1:9/api/v1/events", maxsize=maxsize)
    transport._closed.set()
    transport._worker.join(timeout=1)
    return transport


class QueueFullTest(unittest.TestCase):
    def test_a_full_queue_counts_every_drop_and_warns_once(self) -> None:
        transport = _stopped_transport()
        err = io.StringIO()
        with redirect_stderr(err):
            self.assertTrue(transport.submit(_event()))
            self.assertFalse(transport.submit(_event()))
            self.assertFalse(transport.submit(_event()))
        self.assertEqual(transport.dropped, 2)
        self.assertEqual(err.getvalue().count("queue full"), 1)
        self.assertIn("event dropped", err.getvalue())

    def test_a_dropped_check_in_is_counted(self) -> None:
        transport = _stopped_transport()
        self.assertTrue(transport.submit_check_in(CheckIn(slug="a", body={"status": "ok"})))
        self.assertFalse(transport.submit_check_in(CheckIn(slug="a", body={"status": "ok"})))
        self.assertEqual(transport.dropped, 1)

    def test_a_subclass_with_another_warn_signature_still_works(self) -> None:
        class Custom(Transport):
            def _warn_once(self, reason: str) -> None:  # type: ignore[override]
                raise AssertionError("must not be called with the old signature")

        transport = Custom(api_key="k", endpoint="http://127.0.0.1:9/api/v1/events", maxsize=1)
        transport._closed.set()
        transport._worker.join(timeout=1)
        with redirect_stderr(io.StringIO()):
            self.assertTrue(transport.submit(_event()))
            self.assertFalse(transport.submit(_event()))
        self.assertEqual(transport.dropped, 1)

    def test_dropped_count_reads_the_global_transport(self) -> None:
        previous = client._transport
        try:
            client._transport = None
            self.assertEqual(bugbarn.dropped_count(), 0)
            client._transport = _stopped_transport()
            with redirect_stderr(io.StringIO()):
                client._transport.submit(_event())
                client._transport.submit(_event())
            self.assertEqual(bugbarn.dropped_count(), 1)
        finally:
            client._transport = previous


if __name__ == "__main__":
    unittest.main()
