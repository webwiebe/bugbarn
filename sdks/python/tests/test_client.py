from __future__ import annotations

import json
import sys
import unittest
from unittest import mock

from bugbarn.client import Envelope, Transport, capture_exception, init


class RecordingTransport:
    def __init__(self):
        self.events = []
        self.closed = False

    def submit(self, event):
        self.events.append(event)
        return True

    def close(self):
        self.closed = True


class SDKTests(unittest.TestCase):
    def test_capture_exception_uses_transport(self):
        transport = RecordingTransport()
        init(api_key="bb_live_test", transport=transport)

        self.assertTrue(capture_exception(RuntimeError("boom"), tags={"service": "api"}))
        self.assertEqual(len(transport.events), 1)
        self.assertEqual(transport.events[0].severityText, "ERROR")
        self.assertEqual(transport.events[0].body, "boom")
        self.assertEqual(transport.events[0].exception_type, "RuntimeError")
        self.assertEqual(transport.events[0].exception_message, "boom")
        self.assertEqual(transport.events[0].tags["service"], "api")
        self.assertEqual(transport.events[0].sender["sdk"]["name"], "bugbarn.python")
        self.assertEqual(transport.events[0].sender["sdk"]["version"], "0.1.0")

    def test_optional_excepthook_installation(self):
        transport = RecordingTransport()
        original = sys.excepthook

        try:
            init(api_key="bb_live_test", transport=transport, install_excepthook=True)
            self.assertIsNot(sys.excepthook, original)
        finally:
            sys.excepthook = original

    def test_transport_sets_api_key_header(self):
        captured = {}

        class Response:
            def __enter__(self):
                return self

            def __exit__(self, exc_type, exc, tb):
                return False

            def read(self):
                return b""

        def fake_urlopen(request, timeout=0):
            captured["url"] = request.full_url
            captured["headers"] = dict(request.header_items())
            captured["body"] = request.data
            return Response()

        transport = Transport(api_key="bb_live_test", endpoint="http://127.0.0.1:9000/api/v1/events")
        event = Envelope(
            timestamp="2026-04-15T12:00:00+00:00",
            severityText="ERROR",
            body="boom",
            exception_type="RuntimeError",
            exception_message="boom",
            sender={"sdk": {"name": "bugbarn.python", "version": "0.1.0"}},
        )

        with mock.patch("urllib.request.urlopen", side_effect=fake_urlopen):
            transport._send(event)

        self.assertEqual(captured["url"], "http://127.0.0.1:9000/api/v1/events")
        self.assertEqual(captured["headers"].get("X-bugbarn-api-key"), "bb_live_test")
        payload = json.loads(captured["body"].decode("utf-8"))
        self.assertEqual(payload["severityText"], "ERROR")
        self.assertEqual(payload["body"], "boom")
        self.assertEqual(payload["exception"]["type"], "RuntimeError")
        self.assertEqual(payload["exception"]["message"], "boom")
        self.assertEqual(payload["sender"]["sdk"]["name"], "bugbarn.python")
        transport.close()


if __name__ == "__main__":
    unittest.main()
