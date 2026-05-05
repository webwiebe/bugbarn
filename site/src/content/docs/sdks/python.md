---
title: Python SDK
description: Integrate BugBarn error tracking into Python applications.
---

# Python SDK

The Python SDK (`bugbarn-python`) is a lightweight client with no external dependencies. It runs a background thread for non-blocking event delivery and supports breadcrumbs, user context, and automatic exception hooking.

## Installation

```bash
pip install bugbarn-python
```

Requires Python 3.9+.

## Initialisation

```python
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
)
```

The SDK registers an `atexit` handler that calls `shutdown()` automatically when the process exits.

### Options reference

| Parameter | Type | Default | Description |
|---|---|---|---|
| `api_key` | `str` | required | Project API key |
| `endpoint` | `str` | `/api/v1/events` | BugBarn ingest URL |
| `install_excepthook` | `bool` | `False` | Replace `sys.excepthook` to capture unhandled exceptions |
| `transport` | `Transport \| None` | `None` | Custom transport (uses built-in if `None`) |

## Capturing errors

### capture_exception

Capture an exception, string, or any object. Returns `True` if the event was enqueued.

```python
try:
    risky_operation()
except Exception as e:
    bugbarn.capture_exception(e)
```

You can also pass a plain string:

```python
bugbarn.capture_exception("something went wrong")
```

### Capture options

Attach metadata to individual events:

```python
bugbarn.capture_exception(
    e,
    attributes={"order_id": 42, "service": "checkout"},
    tags={"environment": "production"},
    extra={"debug_info": "additional context"},
)
```

| Parameter | Type | Description |
|---|---|---|
| `attributes` | `dict[str, Any]` | Arbitrary key-value metadata |
| `tags` | `dict[str, Any]` | Indexed tags for filtering |
| `extra` | `dict[str, Any]` | Additional unindexed context |

## Automatic exception hook

Set `install_excepthook=True` to automatically capture unhandled exceptions via `sys.excepthook`:

```python
bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
    install_excepthook=True,
)

# Any unhandled exception will now be captured before Python exits
raise ValueError("this will be reported to BugBarn")
```

The original `sys.__excepthook__` is called after capture, so you still get the default traceback output.

## Breadcrumbs

Breadcrumbs record a trail of events leading up to an error. Add them manually:

```python
bugbarn.add_breadcrumb(
    category="http",
    message="GET /api/users",
    level="info",
    data={"status_code": 200},
)
```

Clear breadcrumbs when needed:

```python
bugbarn.clear_breadcrumbs()
```

Up to 100 breadcrumbs are kept. When the limit is reached, the oldest is dropped.

## User context

Attach user information to all subsequent events:

```python
bugbarn.set_user(id="u_123", email="jane@example.com", username="jane")
```

Clear user context on logout:

```python
bugbarn.clear_user()
```

## Flush and shutdown

### flush

Drain all queued events within a timeout. Returns `True` if fully drained.

```python
drained = bugbarn.flush(timeout=5.0)
```

### shutdown

Flush remaining events and stop the background thread.

```python
bugbarn.shutdown(timeout=2.0)
```

`shutdown` is called automatically via `atexit` when the process exits normally.

## Framework integration

### Django

Add BugBarn as Django middleware to capture unhandled exceptions in views:

```python
# settings.py
MIDDLEWARE = [
    "myapp.middleware.BugBarnMiddleware",
    # ... other middleware
]

# myapp/middleware.py
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
)

class BugBarnMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        return self.get_response(request)

    def process_exception(self, request, exception):
        bugbarn.capture_exception(
            exception,
            attributes={
                "path": request.path,
                "method": request.method,
            },
        )
        return None  # let Django's default handling continue
```

### Flask

Use Flask's `errorhandler` to capture exceptions:

```python
from flask import Flask
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
)

app = Flask(__name__)

@app.errorhandler(Exception)
def handle_exception(e):
    bugbarn.capture_exception(e)
    return "Internal Server Error", 500
```

### FastAPI

Use FastAPI middleware to capture exceptions:

```python
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
)

app = FastAPI()

@app.middleware("http")
async def bugbarn_middleware(request: Request, call_next):
    try:
        return await call_next(request)
    except Exception as e:
        bugbarn.capture_exception(
            e,
            attributes={
                "path": str(request.url.path),
                "method": request.method,
            },
        )
        return JSONResponse(status_code=500, content={"detail": "Internal Server Error"})
```

## Full example

```python
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
    install_excepthook=True,
)

bugbarn.set_user(id="u_42", email="dev@example.com")

bugbarn.add_breadcrumb(
    category="startup",
    message="Application initialized",
    level="info",
)

try:
    result = 1 / 0
except ZeroDivisionError as e:
    bugbarn.capture_exception(
        e,
        attributes={"component": "math"},
        tags={"severity": "low"},
    )

bugbarn.shutdown(timeout=2.0)
```
