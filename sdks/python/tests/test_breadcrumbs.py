from __future__ import annotations

import unittest

from bugbarn.breadcrumbs import add_breadcrumb, get_breadcrumbs, clear_breadcrumbs, MAX_BREADCRUMBS
from bugbarn.client import init, shutdown, capture_exception


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


class BreadcrumbTests(unittest.TestCase):
    def setUp(self):
        clear_breadcrumbs()

    def tearDown(self):
        clear_breadcrumbs()
        shutdown()

    def test_add_breadcrumb_adds_entry(self):
        add_breadcrumb(category="navigation", message="user navigated to /home")
        crumbs = get_breadcrumbs()
        self.assertEqual(len(crumbs), 1)
        self.assertEqual(crumbs[0].category, "navigation")
        self.assertEqual(crumbs[0].message, "user navigated to /home")

    def test_buffer_caps_at_max(self):
        for i in range(MAX_BREADCRUMBS + 1):
            add_breadcrumb(category="test", message=f"event {i}")
        crumbs = get_breadcrumbs()
        self.assertEqual(len(crumbs), MAX_BREADCRUMBS)
        # The oldest entry should have been dropped; last message should be the newest
        self.assertEqual(crumbs[-1].message, f"event {MAX_BREADCRUMBS}")

    def test_clear_breadcrumbs_empties_buffer(self):
        add_breadcrumb(category="test", message="something")
        clear_breadcrumbs()
        self.assertEqual(get_breadcrumbs(), [])

    def test_breadcrumbs_appear_in_captured_event_payload(self):
        transport = RecordingTransport()
        init(api_key="bb_test", transport=transport)

        add_breadcrumb(category="http", message="GET /api/users", level="info", data={"status": 200})
        add_breadcrumb(category="db", message="SELECT * FROM users")

        capture_exception(RuntimeError("breadcrumb test"))

        self.assertEqual(len(transport.events), 1)
        payload = transport.events[0].to_payload()
        self.assertIn("breadcrumbs", payload)
        self.assertEqual(len(payload["breadcrumbs"]), 2)
        self.assertEqual(payload["breadcrumbs"][0]["category"], "http")
        self.assertEqual(payload["breadcrumbs"][0]["message"], "GET /api/users")
        self.assertEqual(payload["breadcrumbs"][0]["level"], "info")
        self.assertEqual(payload["breadcrumbs"][0]["data"], {"status": 200})
        self.assertEqual(payload["breadcrumbs"][1]["category"], "db")


if __name__ == "__main__":
    unittest.main()
