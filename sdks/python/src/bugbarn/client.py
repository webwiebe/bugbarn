from __future__ import annotations

import atexit
import datetime
import json
import queue
import sys
import threading
import traceback
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any, List, Optional

from .breadcrumbs import Breadcrumb, get_breadcrumbs
from .user import UserContext, get_user

SDK_NAME = "bugbarn.python"
SDK_VERSION = "1.2.1"
DEFAULT_ENDPOINT = "/api/v1/events"


@dataclass
class StackFrame:
    function: str | None = None
    file: str | None = None
    line: int | None = None
    column: int | None = None
    module: str | None = None


@dataclass
class Envelope:
    timestamp: str
    severityText: str
    body: str
    exception_type: str
    exception_message: str
    stacktrace: list[StackFrame] | None = None
    attributes: dict[str, Any] = field(default_factory=dict)
    tags: dict[str, Any] = field(default_factory=dict)
    extra: dict[str, Any] = field(default_factory=dict)
    sender: dict[str, Any] = field(
        default_factory=lambda: {"sdk": {"name": SDK_NAME, "version": SDK_VERSION}}
    )
    user: Optional[UserContext] = None
    breadcrumbs: Optional[List[Breadcrumb]] = None
    trace_id: Optional[str] = None

    def to_payload(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "timestamp": self.timestamp,
            "severityText": self.severityText,
            "body": self.body,
            "exception": {
                "type": self.exception_type,
                "message": self.exception_message,
            },
            "attributes": self.attributes,
            "tags": self.tags,
            "extra": self.extra,
            "sender": self.sender,
        }
        if self.stacktrace:
            payload["exception"]["stacktrace"] = [frame.__dict__ for frame in self.stacktrace]
        if self.trace_id:
            payload["traceId"] = self.trace_id
        if self.user:
            payload["user"] = self.user.to_dict()
        if self.breadcrumbs:
            payload["breadcrumbs"] = [c.to_dict() for c in self.breadcrumbs]
        return payload


class Transport:
    def __init__(
        self,
        api_key: str,
        endpoint: str = DEFAULT_ENDPOINT,
        maxsize: int = 256,
        project: str | None = None,
    ) -> None:
        self.api_key = api_key
        self.project = project
        if endpoint and "/api/" not in endpoint:
            self.endpoint = endpoint.rstrip("/") + DEFAULT_ENDPOINT
        else:
            self.endpoint = endpoint
        self.queue: "queue.Queue[Envelope]" = queue.Queue(maxsize=maxsize)
        self._closed = threading.Event()
        self._warned = False
        self._worker = threading.Thread(target=self._run, name="bugbarn-transport", daemon=True)
        self._worker.start()

    def submit(self, event: Envelope) -> bool:
        try:
            self.queue.put_nowait(event)
            return True
        except queue.Full:
            return False

    def flush(self, timeout: float = 2.0) -> bool:
        """Drain the in-flight queue within *timeout* seconds. Returns True if drained."""
        joiner = threading.Thread(target=self.queue.join, daemon=True)
        joiner.start()
        joiner.join(timeout=timeout)
        return not joiner.is_alive()

    def close(self, timeout: float = 2.0) -> bool:
        """Flush remaining events then stop the transport thread."""
        drained = self.flush(timeout=timeout)
        self._closed.set()
        self._worker.join(timeout=0.1)
        return drained

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

    def _send(self, event: Envelope) -> None:
        payload = json.dumps(event.to_payload()).encode("utf-8")
        headers = {
            "content-type": "application/json",
            "x-bugbarn-api-key": self.api_key,
        }
        if self.project:
            headers["x-bugbarn-project"] = self.project
        request = urllib.request.Request(self.endpoint, data=payload, method="POST", headers=headers)

        try:
            with urllib.request.urlopen(request, timeout=2) as response:
                response.read()
        except urllib.error.HTTPError as err:
            self._warn_once(f"ingest answered HTTP {err.code}")
        except urllib.error.URLError as err:
            self._warn_once(f"ingest unreachable: {err.reason}")

    def _warn_once(self, reason: str) -> None:
        if self._warned:
            return
        self._warned = True
        print(f"bugbarn: event dropped, {reason} ({self.endpoint})", file=sys.stderr)


_transport: Transport | None = None
_install_hook = False
_atexit_registered = False
_default_attributes: dict[str, Any] = {}


def _with_environment(attributes: dict[str, Any]) -> dict[str, Any]:
    """Mirror the environment into ``deployment.environment``, the OpenTelemetry key
    the server turns into the environment facet. A caller-set ``deployment.environment``
    wins, and a per-call ``environment`` flows into the mirror like the TypeScript SDK."""
    environment = attributes.get("environment")
    if environment and "deployment.environment" not in attributes:
        attributes["deployment.environment"] = environment
    return attributes


def _normalize_exception(exc: BaseException | str | object) -> BaseException:
    if isinstance(exc, BaseException):
        return exc
    if isinstance(exc, str):
        return Exception(exc)
    return Exception("Unknown error")


def _extract_stacktrace(tb) -> list[StackFrame] | None:
    if tb is None:
        return None

    frames: list[StackFrame] = []
    for frame in traceback.extract_tb(tb):
        module = frame.filename.rsplit("/", 1)[-1] if frame.filename else None
        frames.append(
            StackFrame(
                function=frame.name or None,
                file=frame.filename or None,
                line=frame.lineno or None,
                column=None,
                module=module,
            )
        )
    return frames or None


def _current_trace_id() -> str | None:
    """Trace id of the active OpenTelemetry span, when opentelemetry is installed."""
    try:
        from opentelemetry import trace
    except ImportError:
        return None
    try:
        ctx = trace.get_current_span().get_span_context()
    except Exception:
        return None
    if not ctx.is_valid:
        return None
    return format(ctx.trace_id, "032x")


def _now_iso() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def _build_event(
    exc: BaseException | str | object,
    *,
    attributes: Optional[dict[str, Any]] = None,
    tags: Optional[dict[str, Any]] = None,
    extra: Optional[dict[str, Any]] = None,
) -> Envelope:
    normalized = _normalize_exception(exc)
    crumbs = get_breadcrumbs()
    return Envelope(
        timestamp=_now_iso(),
        severityText="ERROR",
        body=str(normalized),
        exception_type=type(normalized).__name__,
        exception_message=str(normalized),
        stacktrace=_extract_stacktrace(normalized.__traceback__),
        attributes=_with_environment({**_default_attributes, **(attributes or {})}),
        tags=tags or {},
        extra=extra or {},
        user=get_user(),
        breadcrumbs=crumbs if crumbs else None,
        trace_id=_current_trace_id(),
    )


def _excepthook(exc_type, exc, tb) -> None:
    capture_exception(exc)
    # The interpreter exits right after this hook, so drain now; the daemon
    # transport thread would otherwise take the event down with it.
    flush(timeout=2.0)
    sys.__excepthook__(exc_type, exc, tb)


def _install_threading_hook() -> None:
    previous = threading.excepthook

    def hook(args) -> None:
        if args.exc_value is not None and args.exc_type is not SystemExit:
            capture_exception(args.exc_value)
        previous(args)

    threading.excepthook = hook


def init(
    *,
    api_key: str,
    endpoint: str = DEFAULT_ENDPOINT,
    install_excepthook: bool = False,
    transport: Transport | None = None,
    project: str | None = None,
    environment: str | None = None,
    release: str | None = None,
) -> None:
    """Start reporting. *project* is sent as the X-BugBarn-Project header (needed by
    setup keys); *environment* and *release* become attributes on every event."""
    global _transport, _install_hook, _atexit_registered, _default_attributes
    _transport = transport or Transport(api_key=api_key, endpoint=endpoint, project=project)
    _default_attributes = {
        key: value
        for key, value in (("environment", environment), ("release", release))
        if value
    }
    if install_excepthook and not _install_hook:
        sys.excepthook = _excepthook
        _install_threading_hook()
        _install_hook = True
    if not _atexit_registered:
        atexit.register(lambda: shutdown(timeout=2.0))
        _atexit_registered = True


def flush(timeout: float = 2.0) -> bool:
    """Drain all queued events within *timeout* seconds. Returns True if fully drained."""
    if _transport is None:
        return True
    return _transport.flush(timeout=timeout)


def shutdown(timeout: float = 2.0) -> bool:
    """Flush queued events, stop the background worker, and detach the global transport."""
    global _transport
    if _transport is None:
        return True
    drained = _transport.close(timeout=timeout)
    _transport = None
    return drained


def capture_exception(
    exc: BaseException | str | object,
    *,
    attributes: Optional[dict[str, Any]] = None,
    tags: Optional[dict[str, Any]] = None,
    extra: Optional[dict[str, Any]] = None,
) -> bool:
    if _transport is None:
        return False
    return _transport.submit(
        _build_event(exc, attributes=attributes, tags=tags, extra=extra)
    )
