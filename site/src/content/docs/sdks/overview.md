---
title: SDK Overview
description: Overview of BugBarn client SDKs for error tracking.
---

# SDK Overview

BugBarn provides official SDKs for four languages. Each SDK captures errors with stack traces, delivers them asynchronously, and supports breadcrumbs and user context.

## Available SDKs

| SDK | Package | Language | Min version |
|---|---|---|---|
| **[Go](/bugbarn/docs/sdks/go)** | `github.com/wiebe-xyz/bugbarn-go` | Go | 1.22+ |
| **[TypeScript](/bugbarn/docs/sdks/typescript)** | `@bugbarn/typescript` | TypeScript / JavaScript | Node 22+ / any modern browser |
| **[Python](/bugbarn/docs/sdks/python)** | `bugbarn-python` | Python | 3.9+ |
| **[PHP](/bugbarn/docs/sdks/php)** | `bugbarn/bugbarn-php` | PHP | 8.1+ |

All SDKs share the same ingest protocol. You can also send events using plain [HTTP requests](/bugbarn/docs/sdks/http) from any language.

## Common features

Every SDK supports:

- **Error capture** -- catch exceptions and report them with full stack traces
- **Message capture** -- send plain string messages as events
- **User context** -- attach user ID, email, and username to events
- **Breadcrumbs** -- record a trail of actions leading up to an error (up to 100 per event)
- **Attributes** -- attach arbitrary key-value metadata to individual events
- **Async delivery** -- events are queued and sent in the background so they do not block your application
- **Flush / shutdown** -- drain the queue before process exit

## SDK-specific features

| Feature | Go | TypeScript | Python | PHP |
|---|---|---|---|---|
| Panic / uncaught exception handler | RecoverMiddleware | uncaughtException + unhandledRejection | sys.excepthook | set_exception_handler + fatal error handler |
| HTTP middleware | Yes | -- | -- | -- |
| Auto breadcrumbs (console, fetch, XHR, navigation) | -- | Yes | -- | -- |
| Source map uploads | -- | Yes | -- | -- |
| Pino log transport | -- | Yes | -- | -- |
| Release tagging | Yes | Yes | -- | -- |
| Environment tagging | Yes | -- | -- | -- |

## Authentication

All SDKs authenticate using an API key sent in the `X-BugBarn-Api-Key` HTTP header. Create an API key in your project settings. Optionally, set the `X-BugBarn-Project` header (or configure `projectSlug` / `project`) to route events to a specific project.

## Ingest endpoint

All SDKs send events to `/api/v1/events` (or `/api/v1/ingest` as an alias). The payload is a JSON object with the following structure:

```json
{
  "timestamp": "2024-01-15T10:30:00.000Z",
  "severityText": "ERROR",
  "body": "something went wrong",
  "exception": {
    "type": "RuntimeError",
    "message": "something went wrong",
    "stacktrace": [
      { "function": "main", "file": "app.go", "line": 42 }
    ]
  },
  "attributes": {},
  "user": { "id": "u_123", "email": "jane@example.com" },
  "breadcrumbs": [],
  "sender": { "sdk": { "name": "bugbarn.go", "version": "0.1.0" } }
}
```
