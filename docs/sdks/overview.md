# SDK Overview

BugBarn can receive events from your applications through a native SDK or directly over HTTP. This page describes the available integration options, how to pick the right one, and the concepts that apply across all of them.

## Available integrations

| Integration | Package / install | Best for |
|---|---|---|
| **Go SDK** | `go get github.com/wiebe-xyz/bugbarn-go` | Go services and HTTP servers |
| **TypeScript SDK** | `@bugbarn/typescript` (npm) | Node.js backends, browser apps |
| **Python SDK** | `bugbarn-python` (pip) | Python services and scripts |
| **Direct HTTP** | Any HTTP client | Any language without an SDK, scripts, CI pipelines |

## Choosing an integration

Use a **native SDK** when you want automatic panic/exception capture, breadcrumbs, user context, and queued delivery without writing transport code yourself.

Use **direct HTTP** when:
- Your language does not have an SDK yet.
- You are sending events from a shell script, CI job, or infrastructure tool.
- You want full control over the payload shape.

Both approaches POST to the same endpoints and produce the same events in BugBarn.

## API keys

Every ingest request must carry an API key in the `X-BugBarn-Api-Key` header (or the equivalent SDK option).

### Scopes

| Scope | Permissions | When to use |
|---|---|---|
| `ingest` | POST `/api/v1/events` and POST `/api/v1/logs` only | SDKs embedded in applications — one compromised key cannot read your data |
| `full` | All endpoints including read access | Admin scripts, local tooling, CI dashboards |

> **Security:** Always use `ingest` scope for any SDK running in your application or browser. An `ingest` key can only write events — a compromised key cannot read your data. Never embed a `full` scope key in client code.

Always use an **`ingest` scope** key for any SDK that runs inside an application process or browser. If the key leaks it cannot be used to read events, issues, or any other data from your BugBarn instance.

### Getting an API key

**Via the UI:** Settings → API Keys → New Key → choose scope `ingest` → copy the key.

**Via the CLI:**

```bash
bugbarn apikey create --scope ingest --name "my-service"
```

## Project scoping

Events are routed to a project in one of two ways:

1. **SDK option** — pass `ProjectSlug` (Go), `project` (TypeScript / Python) when initialising the SDK. The SDK sends the `X-BugBarn-Project` header on every request.
2. **Header directly** — include `X-BugBarn-Project: <slug>` in your HTTP requests.

If neither is provided, events land in the `default` project. BugBarn creates the project automatically on first use, so you do not need to create it in advance.

A single shared API key can route to different projects by changing the header value, which is useful when one service emits events for multiple logical projects.

## Event shape

All events share the same JSON structure regardless of which SDK or HTTP client sends them.

```json
{
  "timestamp": "2026-04-26T10:30:00Z",
  "severityText": "error",
  "body": "human-readable summary of what happened",
  "exception": {
    "type": "ExceptionClassName",
    "message": "exception message",
    "stacktrace": [
      {
        "function": "functionName",
        "module": "moduleName",
        "filename": "file.go",
        "lineno": 42,
        "colno": 5
      }
    ]
  },
  "attributes": { "key": "value" },
  "resource": { "service.name": "api", "host.name": "server-01" },
  "user": { "id": "usr_123", "email": "user@example.com", "username": "alice" },
  "breadcrumbs": [
    { "timestamp": "2026-04-26T10:29:59Z", "message": "fetched order", "level": "info", "data": {} }
  ]
}
```

### Required fields

| Field | Description |
|---|---|
| `timestamp` | ISO 8601 timestamp of when the event occurred |
| `severityText` | One of `trace`, `debug`, `info`, `warn`, `error`, `fatal` |
| `body` | Human-readable message |

### Optional fields

| Field | Description |
|---|---|
| `exception` | Structured exception with type, message, and stacktrace frames |
| `attributes` | Arbitrary key-value metadata (strings, numbers, booleans) |
| `resource` | Infrastructure context — service name, host, region, etc. |
| `user` | Attached user context (id, email, username) |
| `breadcrumbs` | Ordered trail of events leading up to this one |

SDKs populate most of these automatically. When sending directly over HTTP you can include as many or as few fields as are useful.

## Privacy scrubbing

BugBarn automatically scrubs sensitive data from every event **before it is stored**. The original payload is never written to disk.

**Keys** that contain any of the following strings have their value replaced with `[redacted]`:

`authorization`, `cookie`, `password`, `passwd`, `secret`, `token`, `api_key`, `apikey`, `session`, `csrf`, `email`

**String values** that match these patterns are replaced with a type-specific placeholder regardless of the key name:

- Email addresses
- IPv4 addresses
- UUIDs
- Bearer tokens

This means you do not need to pre-scrub event payloads in your application code. Stacktrace frames, breadcrumb messages, and arbitrary attributes are all checked. Any matching value is replaced before the event reaches the database.
