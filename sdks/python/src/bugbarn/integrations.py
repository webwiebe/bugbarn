"""Capture paths for errors that never pass through application code.

``BugBarnHandler`` catches logged errors (uvicorn, FastAPI background tasks,
libraries), ``BugBarnASGIMiddleware`` catches exceptions escaping an ASGI app,
and ``install_asyncio_handler`` catches errors from tasks nobody awaited.
"""

from __future__ import annotations

import asyncio
import logging
from typing import Any

from .client import capture_exception

# Records logged without an exception are reported under this type name.
_LogMessage = type("LogRecord", (Exception,), {})


class BugBarnHandler(logging.Handler):
    """Send every record at ERROR or above to BugBarn.

    Attach it to loggers that do not propagate to the root logger, such as
    ``uvicorn.error``::

        logging.getLogger("uvicorn.error").addHandler(BugBarnHandler())
    """

    def __init__(self, level: int = logging.ERROR) -> None:
        super().__init__(level=level)

    def emit(self, record: logging.LogRecord) -> None:
        try:
            message = record.getMessage()
            exc = record.exc_info[1] if record.exc_info else None
            if exc is None:
                exc = _LogMessage(message)
            capture_exception(
                exc,
                attributes={"logger": record.name, "log.message": message},
            )
        except Exception:
            self.handleError(record)


class BugBarnASGIMiddleware:
    """Report exceptions raised by the wrapped ASGI app, then re-raise them."""

    def __init__(self, app: Any) -> None:
        self.app = app

    async def __call__(self, scope: Any, receive: Any, send: Any) -> None:
        try:
            await self.app(scope, receive, send)
        except Exception as exc:
            capture_exception(exc, attributes=_scope_attributes(scope))
            raise


def _scope_attributes(scope: Any) -> dict[str, Any]:
    attributes: dict[str, Any] = {"asgi.type": scope.get("type", "")}
    if scope.get("method"):
        attributes["http.method"] = scope["method"]
    if scope.get("path"):
        attributes["http.path"] = scope["path"]
    return attributes


def install_asyncio_handler(loop: asyncio.AbstractEventLoop) -> None:
    """Report errors the event loop would only log: unretrieved task results, destroyed tasks."""
    previous = loop.get_exception_handler()

    def handler(loop: asyncio.AbstractEventLoop, context: dict[str, Any]) -> None:
        exc = context.get("exception")
        message = str(context.get("message", ""))
        capture_exception(
            exc if isinstance(exc, BaseException) else _LogMessage(message),
            attributes={"logger": "asyncio", "log.message": message},
        )
        if previous is not None:
            previous(loop, context)
        else:
            loop.default_exception_handler(context)

    loop.set_exception_handler(handler)
