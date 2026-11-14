from __future__ import annotations

import pathlib
import re
import unittest

from support import RecordingTransport, sent_headers

from bugbarn import client
from bugbarn.client import Transport, capture_exception, init, shutdown


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

    def test_environment_is_mirrored_to_deployment_environment(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production")

        capture_exception(RuntimeError("boom"))

        attrs = transport.events[0].attributes
        self.assertEqual(attrs["environment"], "production")
        self.assertEqual(attrs["deployment.environment"], "production")

    def test_per_call_environment_flows_into_the_mirror(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production")

        capture_exception(RuntimeError("boom"), attributes={"environment": "canary"})

        attrs = transport.events[0].attributes
        self.assertEqual(attrs["deployment.environment"], "canary")

    def test_caller_set_deployment_environment_wins(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, environment="production")

        capture_exception(RuntimeError("boom"), attributes={"deployment.environment": "eu-west"})

        attrs = transport.events[0].attributes
        self.assertEqual(attrs["environment"], "production")
        self.assertEqual(attrs["deployment.environment"], "eu-west")

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
        headers = sent_headers(transport)

        self.assertEqual(headers["x-bugbarn-project"], "slimmeagenda")
        self.assertEqual(headers["x-bugbarn-api-key"], "k")

    def test_no_project_means_no_header(self):
        transport = Transport(api_key="k", endpoint="http://127.0.0.1:1/api/v1/events")
        headers = sent_headers(transport)

        self.assertNotIn("x-bugbarn-project", headers)


class VersionTests(unittest.TestCase):
    def test_sdk_version_matches_pyproject(self):
        text = (pathlib.Path(__file__).resolve().parents[1] / "pyproject.toml").read_text()
        declared = re.search(r'^version = "([^"]+)"', text, re.M).group(1)
        self.assertEqual(client.SDK_VERSION, declared)
