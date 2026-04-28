# Direct HTTP Integration

BugBarn exposes two ingest endpoints that accept plain JSON over HTTP. You can use any HTTP client from any language — no SDK required.

## When to use direct HTTP

- Your language does not have a BugBarn SDK yet.
- You are sending events from a shell script, Makefile target, or CI/CD pipeline.
- You want to integrate BugBarn into an existing logging or error-reporting pipeline without adding a dependency.
- You need full control over the exact payload shape.

For Go, TypeScript, or Python applications, the native SDKs handle queueing, retries, and context propagation for you. Direct HTTP is best suited to languages and environments where that overhead is not warranted.

## API key setup

Create an API key with `ingest` scope. This scope permits POST to `/api/v1/events` and POST to `/api/v1/logs` only — a leaked key cannot be used to read any data from your BugBarn instance.

**Via the UI:** Settings → API Keys → New Key → scope: `ingest`.

**Via the CLI:**

```bash
bugbarn apikeys create --scope ingest --name "my-service"
```

Pass the key in the `X-BugBarn-Api-Key` request header on every call.

## POST /api/v1/events

Send a structured error or log event.

### Complete example

```bash
curl -X POST https://bugbarn.example.com/api/v1/events \
  -H "X-BugBarn-Api-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Project: my-app" \
  -d '{
    "timestamp": "2026-04-26T10:30:00Z",
    "severityText": "error",
    "body": "Something went wrong",
    "exception": {
      "type": "TypeError",
      "message": "Cannot read property x of undefined",
      "stacktrace": [
        {"function": "processOrder", "filename": "orders.js", "lineno": 42},
        {"function": "handleRequest", "filename": "server.js", "lineno": 18}
      ]
    },
    "attributes": {
      "environment": "production",
      "release": "v1.2.3",
      "http.route": "/api/orders"
    },
    "resource": {
      "service.name": "my-api",
      "host.name": "web-01"
    }
  }'
```

### Field reference

#### Required

| Field | Type | Description |
|---|---|---|
| `timestamp` | string | ISO 8601 timestamp of when the event occurred (e.g. `2026-04-26T10:30:00Z`) |
| `severityText` | string | Severity level — see [Severity values](#severity-values) |
| `body` | string | Human-readable summary of what happened |

#### Optional

| Field | Type | Description |
|---|---|---|
| `exception` | object | Structured exception information |
| `exception.type` | string | Exception class or error type name (e.g. `TypeError`, `ValueError`) |
| `exception.message` | string | Exception message |
| `exception.stacktrace` | array | Ordered list of stack frames — see [Stack frame format](#stack-frame-format) |
| `attributes` | object | Arbitrary key-value metadata. Values may be strings, numbers, or booleans |
| `resource` | object | Infrastructure context. Common keys: `service.name`, `host.name`, `cloud.region` |
| `user` | object | Attached user — `id`, `email`, `username` (all optional within the object) |
| `breadcrumbs` | array | Trail of events leading up to this one — see [Breadcrumbs format](#breadcrumbs-format) |

### Severity values

`severityText` must be one of the following (case-insensitive):

| Value | When to use |
|---|---|
| `trace` | Extremely detailed diagnostic output |
| `debug` | Development-time diagnostic information |
| `info` | Normal operational events worth recording |
| `warn` | Something unexpected happened but the application continued |
| `error` | An operation failed; requires attention |
| `fatal` | The application is about to crash or has entered an unrecoverable state |

### Stack frame format

Each entry in `exception.stacktrace` is an object. All fields are optional.

```json
{
  "function": "processOrder",
  "module": "orders",
  "filename": "src/orders.js",
  "lineno": 42,
  "colno": 5
}
```

| Field | Type | Description |
|---|---|---|
| `function` | string | Name of the function or method |
| `module` | string | Module or package name |
| `filename` | string | Source file path |
| `lineno` | integer | Line number in the source file |
| `colno` | integer | Column number in the source file |

Frames should be ordered innermost (where the exception was raised) first. If your language stacks them outermost-first, reverse the array before sending.

### Breadcrumbs format

Breadcrumbs are a time-ordered trail of events that led up to the captured error. BugBarn displays them alongside the event to help reconstruct what the application was doing.

```json
"breadcrumbs": [
  {
    "timestamp": "2026-04-26T10:29:58Z",
    "message": "GET /api/products returned 200",
    "level": "info",
    "data": { "duration_ms": 45 }
  },
  {
    "timestamp": "2026-04-26T10:29:59Z",
    "message": "placed order for product_id=99",
    "level": "info",
    "data": { "product_id": "99" }
  }
]
```

| Field | Type | Required | Description |
|---|---|---|---|
| `timestamp` | string | Yes | ISO 8601 timestamp |
| `message` | string | Yes | Human-readable description |
| `level` | string | No | `trace`, `debug`, `info`, `warn`, `error`, or `fatal` |
| `data` | object | No | Arbitrary key-value context for this breadcrumb |

## POST /api/v1/logs

Send a structured log entry. This endpoint is designed for log forwarding — piping your application's log stream into BugBarn rather than instrumenting individual error sites.

### Example

```bash
curl -X POST https://bugbarn.example.com/api/v1/logs \
  -H "X-BugBarn-Api-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Project: my-app" \
  -d '{
    "level": "error",
    "message": "Database connection failed",
    "data": { "host": "db.internal", "retries": 3 }
  }'
```

## Project routing

Set `X-BugBarn-Project` to the slug of the project you want to receive the event. If the project does not exist it is created automatically. If the header is omitted the event lands in the `default` project.

A single API key can route to any number of projects by varying this header.

## Response codes

| Code | Meaning |
|---|---|
| `202 Accepted` | Event accepted and queued for processing |
| `400 Bad Request` | Malformed JSON or a required field is missing or invalid |
| `401 Unauthorized` | `X-BugBarn-Api-Key` header is missing or the key is not recognised |
| `429 Too Many Requests` | The server-side ingest spool is full. Retry after the number of seconds indicated in the `Retry-After` response header |

On `429`, back off before retrying. A simple strategy is to wait the value in `Retry-After` (if present) or use exponential backoff starting at one second.

Both endpoints use `Access-Control-Allow-Origin: *`, so browser JavaScript can POST to them directly without a proxy.

## Implementation tips

### Never block the main execution path

Deliver events asynchronously. Dropping an event is always preferable to adding latency to user-facing requests.

**Pattern: fire-and-forget in the background**

```python
import threading, requests

def report_error(payload):
    threading.Thread(
        target=lambda: requests.post(
            "https://bugbarn.example.com/api/v1/events",
            json=payload,
            headers={"X-BugBarn-Api-Key": API_KEY, "X-BugBarn-Project": PROJECT},
            timeout=3,
        ),
        daemon=True,
    ).start()
```

### Batch log lines where possible

The `/api/v1/logs` endpoint accepts a `logs` array so you can send multiple entries in a single request:

```bash
curl -X POST https://bugbarn.example.com/api/v1/logs \
  -H "X-BugBarn-Api-Key: your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"logs": [
    {"level": "warn", "message": "retry 1", "data": {}},
    {"level": "error", "message": "retry limit exceeded", "data": {"retries": 3}}
  ]}'
```

Accumulate log lines in a small buffer and flush on a timer (e.g. every second) or when the buffer reaches a threshold (e.g. 50 entries). This reduces HTTP round-trips without meaningfully increasing event latency.

### Set a short timeout

Use a connection and read timeout of 2-5 seconds. If BugBarn is unreachable the request should fail fast rather than holding a thread or goroutine open.

### Respect 429 responses

If the server responds with `429`, pause delivery, check `Retry-After`, and resume after the indicated delay. Do not drop the buffered events unless your buffer is also full.

### Do not retry 400 responses

A `400` indicates a payload the server will never accept. Log the error locally and discard the event — retrying will not help.
