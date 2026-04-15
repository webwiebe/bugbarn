from __future__ import annotations

from dataclasses import dataclass, field
import atexit
import json
import queue
import sys
import threading
import traceback
import urllib.error
import urllib.request
from typing import Any, Optional

SDK_NAME = "bugbarn.python"
DEFAULT_ENDPOINT = "/api/v1/events"


@dataclass
class Event:
    sdk: str
    message: str
    exception_type: str
    exception_value: str
    stack: str | None
    tags: dict[str, Any] = field(default_factory=dict)
    extra: dict[str, Any] = field(default_factory=dict)


class Transport:
    def __init__(self, api_key: str, endpoint: str = DEFAULT_ENDPOINT, maxsize: int = 256) -> None:
        self.api_key = api_key
        self.endpoint = endpoint
        self.queue: "queue.Queue[Event]" = queue.Queue(maxsize=maxsize)
        self._closed = threading.Event()
        self._worker = threading.Thread(target=self._run, name="bugbarn-transport", daemon=True)
        self._worker.start()

    def submit(self, event: Event) -> bool:
        try:
            self.queue.put_nowait(event)
            return True
        except queue.Full:
            return False

    def close(self) -> None:
        self._closed.set()
        self._worker.join(timeout=1.0)

    def _run(self) -> None:
        while not self._closed.is_set():
            try:
                event = self.queue.get(timeout=0.1)
            except queue.Empty:
                continue
            try:
                self._send(event)
            finally:
                self.queue.task_done()

    def _send(self, event: Event) -> None:
        payload = json.dumps({"events": [event.__dict__]}).encode("utf-8")
        request = urllib.request.Request(
            self.endpoint,
            data=payload,
            method="POST",
            headers={
                "content-type": "application/json",
                "x-bugbarn-api-key": self.api_key,
            },
        )

        try:
            with urllib.request.urlopen(request, timeout=2) as response:
                response.read()
        except urllib.error.URLError:
            return


_transport: Transport | None = None
_install_hook = False


def _normalize_exception(exc: BaseException | str | object) -> BaseException:
    if isinstance(exc, BaseException):
        return exc
    if isinstance(exc, str):
        return Exception(exc)
    return Exception("Unknown error")


def _build_event(exc: BaseException | str | object, tags: Optional[dict[str, Any]] = None, extra: Optional[dict[str, Any]] = None) -> Event:
    normalized = _normalize_exception(exc)
    stack = "".join(traceback.format_exception(type(normalized), normalized, normalized.__traceback__))
    return Event(
        sdk=SDK_NAME,
        message=str(normalized),
        exception_type=type(normalized).__name__,
        exception_value=str(normalized),
        stack=stack or None,
        tags=tags or {},
        extra=extra or {},
    )


def _excepthook(exc_type, exc, tb) -> None:
    capture_exception(exc)
    sys.__excepthook__(exc_type, exc, tb)


def init(*, api_key: str, endpoint: str = DEFAULT_ENDPOINT, install_excepthook: bool = False, transport: Transport | None = None) -> None:
    global _transport, _install_hook
    _transport = transport or Transport(api_key=api_key, endpoint=endpoint)
    if install_excepthook and not _install_hook:
        sys.excepthook = _excepthook
        _install_hook = True
    atexit.register(lambda: _transport.close() if _transport else None)


def capture_exception(exc: BaseException | str | object, *, tags: Optional[dict[str, Any]] = None, extra: Optional[dict[str, Any]] = None) -> bool:
    if _transport is None:
        return False
    return _transport.submit(_build_event(exc, tags=tags, extra=extra))
