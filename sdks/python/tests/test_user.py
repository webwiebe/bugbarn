from __future__ import annotations

import unittest

from bugbarn.user import set_user, clear_user, get_user
from bugbarn.client import init, shutdown


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


class UserTests(unittest.TestCase):
    def setUp(self):
        clear_user()

    def tearDown(self):
        clear_user()
        shutdown()

    def test_get_user_returns_none_by_default(self):
        self.assertIsNone(get_user())

    def test_set_user_stores_fields(self):
        set_user(id="123", email="alice@example.com", username="alice")
        u = get_user()
        self.assertIsNotNone(u)
        self.assertEqual(u.id, "123")
        self.assertEqual(u.email, "alice@example.com")
        self.assertEqual(u.username, "alice")

    def test_clear_user_resets_to_none(self):
        set_user(id="123")
        clear_user()
        self.assertIsNone(get_user())

    def test_user_appears_in_captured_event_payload(self):
        transport = RecordingTransport()
        init(api_key="bb_test", transport=transport)

        set_user(id="42", email="bob@example.com", username="bob")

        from bugbarn.client import capture_exception
        capture_exception(RuntimeError("test with user"))

        self.assertEqual(len(transport.events), 1)
        payload = transport.events[0].to_payload()
        self.assertIn("user", payload)
        self.assertEqual(payload["user"]["id"], "42")
        self.assertEqual(payload["user"]["email"], "bob@example.com")
        self.assertEqual(payload["user"]["username"], "bob")


if __name__ == "__main__":
    unittest.main()
