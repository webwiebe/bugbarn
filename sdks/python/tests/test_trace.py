from __future__ import annotations

import unittest
from unittest import mock

from bugbarn.client import capture_exception, init, shutdown


class RecordingTransport:
    def __init__(self):
        self.events = []

    def submit(self, event):
        self.events.append(event)
        return True

    def flush(self, timeout: float = 2.0) -> bool:
        return True

    def close(self, timeout: float = 2.0) -> bool:
        return True


def fake_otel(trace_id: int, valid: bool):
    ctx = mock.Mock(trace_id=trace_id)
    ctx.is_valid = valid
    span = mock.Mock()
    span.get_span_context.return_value = ctx
    trace_mod = mock.Mock()
    trace_mod.get_current_span.return_value = span
    return {"opentelemetry": mock.Mock(trace=trace_mod), "opentelemetry.trace": trace_mod}


class TraceIdTests(unittest.TestCase):
    def setUp(self):
        self.transport = RecordingTransport()
        init(api_key="k", transport=self.transport)

    def tearDown(self):
        shutdown()

    def test_trace_id_comes_from_the_active_span(self):
        with mock.patch.dict("sys.modules", fake_otel(0xABCDEF, True)):
            capture_exception(RuntimeError("boom"))

        event = self.transport.events[0]
        self.assertEqual(event.trace_id, "00000000000000000000000000abcdef")
        self.assertEqual(event.to_payload()["traceId"], "00000000000000000000000000abcdef")

    def test_no_active_span_omits_trace_id(self):
        with mock.patch.dict("sys.modules", fake_otel(0, False)):
            capture_exception(RuntimeError("boom"))

        self.assertIsNone(self.transport.events[0].trace_id)
        self.assertNotIn("traceId", self.transport.events[0].to_payload())

    def test_missing_opentelemetry_is_fine(self):
        with mock.patch.dict("sys.modules", {"opentelemetry": None}):
            capture_exception(RuntimeError("boom"))

        self.assertIsNone(self.transport.events[0].trace_id)
