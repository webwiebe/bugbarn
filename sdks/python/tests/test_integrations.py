from __future__ import annotations

import asyncio
import io
import logging
import sys
import threading
import unittest
from contextlib import redirect_stderr
from unittest import mock

import bugbarn
from bugbarn import client
from bugbarn.client import Transport, init, shutdown
from bugbarn.integrations import (
    BugBarnASGIMiddleware,
    BugBarnHandler,
    install_asyncio_handler,
)


class RecordingTransport:
    def __init__(self):
        self.events = []
        self.flushed = 0

    def submit(self, event):
        self.events.append(event)
        return True

    def flush(self, timeout: float = 2.0) -> bool:
        self.flushed += 1
        return True

    def close(self, timeout: float = 2.0) -> bool:
        return True


class LoggingHandlerTests(unittest.TestCase):
    def setUp(self):
        self.transport = RecordingTransport()
        init(api_key="k", transport=self.transport)
        self.logger = logging.getLogger("bugbarn-test-uvicorn")
        self.logger.propagate = False
        self.logger.handlers = [BugBarnHandler()]

    def tearDown(self):
        self.logger.handlers = []
        shutdown()

    def test_error_with_exc_info_is_captured_with_logger_name(self):
        try:
            raise ValueError("bad request body")
        except ValueError:
            self.logger.error("Exception in ASGI application", exc_info=True)

        self.assertEqual(len(self.transport.events), 1)
        event = self.transport.events[0]
        self.assertEqual(event.exception_type, "ValueError")
        self.assertEqual(event.exception_message, "bad request body")
        self.assertEqual(event.attributes["logger"], "bugbarn-test-uvicorn")
        self.assertEqual(event.attributes["log.message"], "Exception in ASGI application")

    def test_error_without_exc_info_is_captured_as_message(self):
        self.logger.error("worker lost job %s", 7)

        event = self.transport.events[0]
        self.assertEqual(event.exception_type, "LogRecord")
        self.assertEqual(event.body, "worker lost job 7")

    def test_below_error_is_ignored(self):
        self.logger.warning("slow")
        self.logger.info("fine")

        self.assertEqual(self.transport.events, [])


class ASGIMiddlewareTests(unittest.TestCase):
    def setUp(self):
        self.transport = RecordingTransport()
        init(api_key="k", transport=self.transport)

    def tearDown(self):
        shutdown()

    def _run(self, app, scope):
        async def go():
            await BugBarnASGIMiddleware(app)(scope, None, None)

        asyncio.run(go())

    def test_exception_is_captured_and_reraised(self):
        async def app(scope, receive, send):
            raise RuntimeError("handler blew up")

        with self.assertRaises(RuntimeError):
            self._run(app, {"type": "http", "method": "GET", "path": "/boom"})

        event = self.transport.events[0]
        self.assertEqual(event.exception_message, "handler blew up")
        self.assertEqual(event.attributes["http.method"], "GET")
        self.assertEqual(event.attributes["http.path"], "/boom")

    def test_clean_request_reports_nothing(self):
        async def app(scope, receive, send):
            return None

        self._run(app, {"type": "http", "method": "GET", "path": "/ok"})

        self.assertEqual(self.transport.events, [])

    def test_lifespan_scope_failure_is_captured(self):
        async def app(scope, receive, send):
            raise RuntimeError("boot failed")

        with self.assertRaises(RuntimeError):
            self._run(app, {"type": "lifespan"})

        self.assertEqual(self.transport.events[0].exception_message, "boot failed")


class AsyncioHandlerTests(unittest.TestCase):
    def setUp(self):
        self.transport = RecordingTransport()
        init(api_key="k", transport=self.transport)

    def tearDown(self):
        shutdown()

    def test_unawaited_task_error_is_captured(self):
        async def go():
            loop = asyncio.get_running_loop()
            install_asyncio_handler(loop)

            async def failing():
                raise KeyError("lost task")

            task = loop.create_task(failing())
            await asyncio.sleep(0)
            await asyncio.sleep(0)
            del task
            import gc

            gc.collect()
            await asyncio.sleep(0)

        asyncio.run(go())

        messages = [e.exception_type for e in self.transport.events]
        self.assertIn("KeyError", messages)

    def test_handler_without_exception_captures_message(self):
        async def go():
            loop = asyncio.get_running_loop()
            install_asyncio_handler(loop)
            loop.call_exception_handler({"message": "Task was destroyed"})

        asyncio.run(go())

        self.assertEqual(self.transport.events[0].body, "Task was destroyed")


class HookTests(unittest.TestCase):
    def setUp(self):
        self.saved_sys = sys.excepthook
        self.saved_thread = threading.excepthook
        client._install_hook = False

    def tearDown(self):
        sys.excepthook = self.saved_sys
        threading.excepthook = self.saved_thread
        client._install_hook = False
        shutdown()

    def test_init_with_excepthook_covers_threads(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, install_excepthook=True)

        def boom():
            raise OSError("thread died")

        with redirect_stderr(io.StringIO()):
            thread = threading.Thread(target=boom)
            thread.start()
            thread.join()

        self.assertEqual(transport.events[0].exception_message, "thread died")

    def test_sys_excepthook_flushes_before_exit(self):
        transport = RecordingTransport()
        init(api_key="k", transport=transport, install_excepthook=True)

        with redirect_stderr(io.StringIO()):
            try:
                raise RuntimeError("boot crash")
            except RuntimeError:
                sys.excepthook(*sys.exc_info())

        self.assertEqual(transport.events[0].exception_message, "boot crash")
        self.assertGreaterEqual(transport.flushed, 1)

    def test_init_registers_atexit_once(self):
        with mock.patch.object(client.atexit, "register") as register:
            client._atexit_registered = False
            init(api_key="k", transport=RecordingTransport())
            init(api_key="k", transport=RecordingTransport())

        self.assertEqual(register.call_count, 1)


class TransportFailureTests(unittest.TestCase):
    def test_non_2xx_response_warns_once_on_stderr(self):
        import urllib.error

        transport = Transport(api_key="bad", endpoint="http://127.0.0.1:1/api/v1/events")
        err = urllib.error.HTTPError("u", 401, "Unauthorized", {}, None)
        out = io.StringIO()
        with mock.patch("urllib.request.urlopen", side_effect=err), redirect_stderr(out):
            from bugbarn.client import Envelope

            event = Envelope(
                timestamp="t", severityText="ERROR", body="b", exception_type="E", exception_message="m"
            )
            transport._send(event)
            transport._send(event)
        transport.close()

        self.assertEqual(out.getvalue().count("401"), 1)
        self.assertIn("bugbarn", out.getvalue())


class PublicApiTests(unittest.TestCase):
    def test_integrations_are_exported(self):
        self.assertTrue(hasattr(bugbarn, "BugBarnHandler"))
        self.assertTrue(hasattr(bugbarn, "BugBarnASGIMiddleware"))
        self.assertTrue(hasattr(bugbarn, "install_asyncio_handler"))
