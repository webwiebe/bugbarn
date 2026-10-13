from __future__ import annotations

import datetime
import json
import sys
import unittest
from unittest import mock

from bugbarn import client
from bugbarn.client import (
    CheckIn,
    Envelope,
    Transport,
    capture_exception,
    check_in,
    flush,
    init,
    shutdown,
)


class RecordingTransport:
    def __init__(self):
        self.events = []
        self.check_ins = []
        self.closed = False
        self.flush_timeout = None

    def submit(self, event):
        self.events.append(event)
        return True

    def flush(self, timeout: float = 2.0) -> bool:
        self.flush_timeout = timeout
        return True

    def submit_check_in(self, item):
        self.check_ins.append(item)
        return True

    def close(self, timeout: float = 2.0):
        self.closed = True
        self.flush_timeout = timeout
        return True


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
        self.assertEqual(transport.events[0].sender["sdk"]["version"], client.SDK_VERSION)

    def test_flush_and_shutdown_use_bounded_timeout(self):
        transport = RecordingTransport()
        init(api_key="bb_live_test", transport=transport)

        self.assertTrue(flush(timeout=0.25))
        self.assertEqual(transport.flush_timeout, 0.25)
        self.assertTrue(shutdown(timeout=0.5))
        self.assertTrue(transport.closed)
        self.assertEqual(transport.flush_timeout, 0.5)
        self.assertFalse(capture_exception(RuntimeError("after shutdown")))

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


class CheckInTests(unittest.TestCase):
    def tearDown(self):
        shutdown(timeout=0.1)

    def test_not_initialised_returns_false(self):
        self.assertFalse(check_in("nightly"))

    def test_rejects_unknown_status(self):
        init(api_key="bb_live_test", transport=RecordingTransport())
        with self.assertRaises(ValueError):
            check_in("nightly", "fine")

    def test_body_carries_status_interval_grace_and_message(self):
        transport = RecordingTransport()
        init(api_key="bb_live_test", transport=transport)

        self.assertTrue(check_in("nightly", "error", interval=3600, grace=datetime.timedelta(minutes=10), message="disk full"))
        self.assertTrue(check_in("nightly", interval="1h"))
        self.assertTrue(check_in("nightly"))

        first, second, third = transport.check_ins
        self.assertEqual(first.slug, "nightly")
        self.assertEqual(first.body, {"status": "error", "interval": "3600s", "grace": "600s", "message": "disk full"})
        self.assertEqual(second.body, {"status": "ok", "interval": "1h"})
        self.assertEqual(third.body, {"status": "ok"})

    def test_body_carries_schedule_and_timezone(self):
        transport = RecordingTransport()
        init(api_key="bb_live_test", transport=transport)

        self.assertTrue(check_in("nightly", schedule="0 6 * * *", timezone="Europe/Amsterdam", grace="10m"))
        self.assertTrue(check_in("nightly", schedule="0 6 * * *"))

        first, second = transport.check_ins
        self.assertEqual(
            first.body,
            {"status": "ok", "grace": "10m", "schedule": "0 6 * * *", "timezone": "Europe/Amsterdam"},
        )
        self.assertEqual(second.body, {"status": "ok", "schedule": "0 6 * * *"})

    def test_schedule_excludes_interval_and_timezone_needs_schedule(self):
        init(api_key="bb_live_test", transport=RecordingTransport())
        with self.assertRaises(ValueError):
            check_in("nightly", interval=3600, schedule="0 6 * * *")
        with self.assertRaises(ValueError):
            check_in("nightly", timezone="Europe/Amsterdam")

    def test_rejects_non_positive_interval(self):
        init(api_key="bb_live_test", transport=RecordingTransport())
        with self.assertRaises(ValueError):
            check_in("nightly", interval=0)

    def test_transport_posts_to_the_monitor_endpoint_with_headers(self):
        captured = {}

        class Response:
            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self):
                return b""

        def fake_urlopen(request, timeout=0):
            captured["url"] = request.full_url
            captured["method"] = request.get_method()
            captured["headers"] = dict(request.header_items())
            captured["body"] = json.loads(request.data.decode("utf-8"))
            return Response()

        transport = Transport(api_key="bb_live_test", endpoint="https://bugbarn.example.com/api/v1/events", project="geo")
        with mock.patch("urllib.request.urlopen", side_effect=fake_urlopen):
            transport._send(CheckIn(slug="pending sweep", body={"status": "ok"}))

        self.assertEqual(captured["url"], "https://bugbarn.example.com/api/v1/monitors/pending%20sweep/check-ins")
        self.assertEqual(captured["method"], "POST")
        self.assertEqual(captured["headers"].get("X-bugbarn-api-key"), "bb_live_test")
        self.assertEqual(captured["headers"].get("X-bugbarn-project"), "geo")
        self.assertEqual(captured["body"], {"status": "ok"})
        self.assertTrue(captured["headers"].get("User-agent", "").startswith("bugbarn-python/"))
        transport.close()

    def test_rejected_requests_warn_once_per_reason_with_the_server_message(self):
        import io
        import urllib.error

        def reject(code, text):
            def fake(request, timeout=0):
                raise urllib.error.HTTPError(request.full_url, code, "x", {}, io.BytesIO(text))
            return fake

        transport = Transport(api_key="bb_live_test", endpoint="https://bugbarn.example.com")
        err = io.StringIO()
        with mock.patch("sys.stderr", err):
            with mock.patch("urllib.request.urlopen", side_effect=reject(400, b"grace must not exceed the interval")):
                transport._send(CheckIn(slug="a", body={"status": "ok"}))
                transport._send(CheckIn(slug="a", body={"status": "ok"}))
            with mock.patch("urllib.request.urlopen", side_effect=reject(403, b"error code: 1010")):
                transport._send(CheckIn(slug="a", body={"status": "ok"}))
        lines = [line for line in err.getvalue().splitlines() if line.startswith("bugbarn:")]
        self.assertEqual(len(lines), 2, lines)
        self.assertIn("HTTP 400 (grace must not exceed the interval)", lines[0])
        self.assertIn("HTTP 403 (error code: 1010)", lines[1])
        transport.close()

    def test_check_in_travels_the_queue_and_flush_waits_for_it(self):
        sent = []
        transport = Transport(api_key="bb_live_test", endpoint="https://bugbarn.example.com")
        transport._send = sent.append
        init(api_key="bb_live_test", transport=transport)

        self.assertTrue(check_in("nightly", interval=60))
        self.assertTrue(flush(timeout=2.0))
        self.assertEqual(len(sent), 1)
        self.assertEqual(sent[0].slug, "nightly")


if __name__ == "__main__":
    unittest.main()
