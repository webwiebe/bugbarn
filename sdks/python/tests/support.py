"""Test doubles shared by the SDK tests."""

from __future__ import annotations

import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

from bugbarn.client import Envelope


class RecordingTransport:
    """A transport that keeps every submitted event in memory."""

    def __init__(self):
        self.events = []
        self.flushed = 0
        self.flush_timeout = None

    def submit(self, event):
        self.events.append(event)
        return True

    def flush(self, timeout: float = 2.0) -> bool:
        self.flushed += 1
        self.flush_timeout = timeout
        return True

    def close(self, timeout: float = 2.0) -> bool:
        return True


def wait_for(condition, seconds: float = 8.0) -> bool:
    """Poll *condition* until it holds or *seconds* pass."""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if condition():
            return True
        time.sleep(0.05)
    return False


class StubIngest:
    """A local HTTP ingest. Subclasses decide the answer in :meth:`respond`.

    ``received`` lists the request paths that were answered, ``first`` is set when
    the first request has been read.
    """

    def __init__(self) -> None:
        self.received: list[str] = []
        self.first = threading.Event()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("content-length", 0))
                self.rfile.read(length)
                owner.respond(self)

            def log_message(self, *args: object) -> None:
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def respond(self, handler: BaseHTTPRequestHandler) -> None:
        raise NotImplementedError

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()


def make_event() -> Envelope:
    return Envelope(
        timestamp="2026-10-31T00:00:00Z",
        severityText="ERROR",
        body="boom",
        exception_type="Exception",
        exception_message="boom",
    )


def sent_headers(transport, event=None) -> dict[str, str]:
    """Send *event* through *transport* with urlopen stubbed and return the request headers."""
    captured: dict[str, str] = {}

    def fake_urlopen(request, timeout=0):
        captured.update({k.lower(): v for k, v in request.header_items()})
        raise OSError("stop")

    with mock.patch("urllib.request.urlopen", side_effect=fake_urlopen):
        try:
            transport._send(event or make_event())
        except OSError:
            pass
    transport.close()
    return captured
