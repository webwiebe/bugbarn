from __future__ import annotations

import pathlib
import re
import unittest
from unittest import mock

from bugbarn import client
from bugbarn.client import Envelope, Transport, capture_exception, init, shutdown


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


class InitContextTests(unittest.TestCase):
    def tearDown(self):
        shutdown()

    def test_environment_and_release_become_default_attributes(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production", release="v1.2.3")

        capture_exception(RuntimeError("boom"))

        attrs = transport.events[0].attributes
        self.assertEqual(attrs["environment"], "production")
        self.assertEqual(attrs["release"], "v1.2.3")

    def test_per_call_attributes_win_over_defaults(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production")

        capture_exception(RuntimeError("boom"), attributes={"environment": "canary"})

        self.assertEqual(transport.events[0].attributes["environment"], "canary")

    def test_unset_context_adds_no_attributes(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport)

        capture_exception(RuntimeError("boom"))

        self.assertEqual(transport.events[0].attributes, {})

    def test_reinit_without_context_clears_it(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production")
        init(api_key="k", transport=transport)

        capture_exception(RuntimeError("boom"))

        self.assertEqual(transport.events[0].attributes, {})

    def test_project_is_sent_as_header(self):
        transport = Transport(api_key="k", endpoint="http://127.0.0.1:1/api/v1/events", project="slimmeagenda")
        captured = {}

        def fake_urlopen(request, timeout):
            captured["headers"] = {k.lower(): v for k, v in request.header_items()}
            raise OSError("stop")

        event = Envelope(timestamp="t", severityText="ERROR", body="b", exception_type="E", exception_message="m")
        with mock.patch("urllib.request.urlopen", side_effect=fake_urlopen):
            try:
                transport._send(event)
            except OSError:
                pass
        transport.close()

        self.assertEqual(captured["headers"]["x-bugbarn-project"], "slimmeagenda")
        self.assertEqual(captured["headers"]["x-bugbarn-api-key"], "k")

    def test_no_project_means_no_header(self):
        transport = Transport(api_key="k", endpoint="http://127.0.0.1:1/api/v1/events")
        captured = {}

        def fake_urlopen(request, timeout):
            captured["headers"] = {k.lower(): v for k, v in request.header_items()}
            raise OSError("stop")

        event = Envelope(timestamp="t", severityText="ERROR", body="b", exception_type="E", exception_message="m")
        with mock.patch("urllib.request.urlopen", side_effect=fake_urlopen):
            try:
                transport._send(event)
            except OSError:
                pass
        transport.close()

        self.assertNotIn("x-bugbarn-project", captured["headers"])


class VersionTests(unittest.TestCase):
    def test_sdk_version_matches_pyproject(self):
        text = (pathlib.Path(__file__).resolve().parents[1] / "pyproject.toml").read_text()
        declared = re.search(r'^version = "([^"]+)"', text, re.M).group(1)
        self.assertEqual(client.SDK_VERSION, declared)
