# BugBarn REST API Reference

## Base URL

All endpoints are prefixed with `/api/v1`.

---

## Authentication

BugBarn supports two authentication mechanisms. Which one is required depends on the endpoint group.

### API Key (X-BugBarn-Api-Key header)

Pass the API key in the `X-BugBarn-Api-Key` request header:

```
X-BugBarn-Api-Key: <your-api-key>
```

API keys have one of two scopes:

| Scope | Access |
|---|---|
| `ingest` | May only call `POST /api/v1/events` and `POST /api/v1/logs`. |
| `full` | May call all protected endpoints. |

Keys are created with `bugbarn apikey create` or via `POST /api/v1/apikeys`. The plaintext key is shown only once at creation time.

### Session Cookie

Browser clients authenticate by calling `POST /api/v1/login` with a JSON body containing `username` and `password`. On success, BugBarn sets two cookies:

- `bugbarn_session` — HttpOnly session token.
- `bugbarn_csrf` — readable CSRF token derived from the session.

### CSRF Token

State-changing requests made with a session cookie (POST, PUT, DELETE — excluding `/api/v1/login`, `/api/v1/logout`, and `/api/v1/events`) must include the CSRF token in the `X-BugBarn-CSRF` header:

```
X-BugBarn-CSRF: <value of the bugbarn_csrf cookie>
```

Requests authenticated via API key are exempt from CSRF checks.

---

## Project Scoping

Most endpoints operate in the context of a project.

### X-BugBarn-Project Header

Send the project slug in this header to scope the request to that project:

```
X-BugBarn-Project: my-project
```

If the slug does not exist, BugBarn creates the project automatically.

### All-Projects Mode

Session-authenticated `GET` requests that omit the `X-BugBarn-Project` header return results from all projects in this BugBarn instance. Each result carries a `project_slug` field identifying which project it belongs to. This is how the web UI shows a global issue list.

### API Key Scoping

When using an API key that was created for a specific project, that project is the implicit scope. The `X-BugBarn-Project` header takes precedence and overrides the key's project binding.

---

## Error Responses

All error responses use plain text bodies with standard HTTP status codes. Successful responses are JSON.

| Status | Meaning |
|---|---|
| `400` | Bad request — malformed or missing payload. |
| `401` | Unauthorized — no valid session or API key. |
| `403` | Forbidden — ingest-only key on a protected endpoint, or missing/invalid CSRF token. |
| `404` | Not found. |
| `405` | Method not allowed. |
| `413` | Payload too large — body exceeds `BUGBARN_MAX_BODY_BYTES`. |
| `429` | Too many requests — spool full (ingest) or login rate limit exceeded. Includes `Retry-After` header. |
| `500` | Internal server error. |

Error body (plain text):

```
unauthorized
```

---

## Rate Limiting

- **Ingest (`POST /api/v1/events`, `POST /api/v1/logs`):** Returns `429` when the spool exceeds `BUGBARN_MAX_SPOOL_BYTES`. Includes `Retry-After: 60`. No other ingest rate limiting applies.
- **Login (`POST /api/v1/login`):** Rate-limited to 10 attempts per IP per minute. Returns `429` with `Retry-After: 60`.
- No rate limiting applies to any other endpoint.

---

## Endpoint Reference

### Public Endpoints (no auth required)

#### GET /api/v1/health

Health check. Always returns `200` when the server is running.

**Response:**
```json
{"status": "ok"}
```

---

#### POST /api/v1/login

Authenticate and obtain a session cookie.

**Request body:**
```json
{"username": "admin", "password": "hunter2"}
```

**Response (success):**
```json
{"authenticated": true, "authEnabled": true, "username": "admin"}
```

Sets `bugbarn_session` and `bugbarn_csrf` cookies on success.

---

#### POST /api/v1/logout

Clear the session cookie.

**Response:**
```json
{"authenticated": false}
```

---

#### GET /api/v1/me

Return the current authentication state.

**Response (authenticated):**
```json
{"authenticated": true, "authEnabled": true, "username": "admin"}
```

**Response (unauthenticated):**
```json
{"authenticated": false, "authEnabled": true}
```

If authentication is not configured, `authEnabled` is `false` and `authenticated` is always `true`.

---

### Ingest Endpoints

Ingest endpoints accept wildcard CORS (`Access-Control-Allow-Origin: *`) so browser SDKs can call them from any origin. Authentication requires an API key with `ingest` or `full` scope.

#### POST /api/v1/events

Ingest an error event.

**Auth:** API key (`ingest` or `full` scope).

**Request body:** Event JSON object (see [Event Payload](#event-payload)).

**Response `202`:**
```json
{"accepted": true, "ingestId": "01HX..."}
```

The event is written to the spool and processed asynchronously. The `ingestId` can be used to correlate spool entries during debugging.

---

#### POST /api/v1/logs

Ingest one or more log entries.

**Auth:** API key (`ingest` or `full` scope).

**Content-Type options:**

- `application/json` — body: `{"logs": [<entry>, ...]}`
- `application/x-ndjson` — one JSON object per line (newline-delimited JSON)

**Request body (JSON):**
```json
{
  "logs": [
    {
      "timestamp": "2026-04-26T10:30:00Z",
      "level": "error",
      "level_num": 50,
      "message": "Database connection failed",
      "data": {
        "host": "db.internal",
        "retries": 3
      }
    }
  ]
}
```

**Response:** `204 No Content` on success.

A project must be determinable from either the API key or the `X-BugBarn-Project` header. Requests without a resolvable project are rejected with `400`.

---

### Issues

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/issues

List issues with optional filtering.

**Query parameters:**

| Parameter | Description |
|---|---|
| `sort` | Sort order: `last_seen` (default), `first_seen`, or `event_count`. |
| `status` | Filter by status: `open`, `muted`, `resolved`, or `all`. |
| `q` | Full-text search query. |
| `<facet_key>=<value>` | Filter by any facet key. Example: `environment=production`. Multiple facets can be combined. |

**Response:**
```json
{
  "issues": [
    {
      "id": "issue-000001",
      "title": "TypeError: Cannot read property 'x' of undefined",
      "status": "open",
      "event_count": 42,
      "first_seen": "2026-04-01T08:00:00Z",
      "last_seen": "2026-04-26T10:30:00Z",
      "hourly_counts": [0, 0, 1, 3, 0, ...]
    }
  ]
}
```

`hourly_counts` is an array of 24 integers representing event counts for each of the last 24 hours (oldest first), used to render a sparkline.

---

#### GET /api/v1/issues/{id}

Fetch a single issue.

**Response:**
```json
{"issue": { ...issue object... }}
```

---

#### GET /api/v1/issues/{id}/events

List events grouped under an issue.

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `limit` | 25 | Maximum number of events to return. |
| `beforeId` | — | Return events with ID less than this value (pagination cursor). |

**Response:**
```json
{"events": [ ...event objects... ]}
```

---

#### POST /api/v1/issues/{id}/resolve

Mark the issue as resolved. No request body.

**Response:**
```json
{"issue": { ...updated issue object... }}
```

---

#### POST /api/v1/issues/{id}/reopen

Reopen the issue to `unresolved`. No request body.

**Response:**
```json
{"issue": { ...updated issue object... }}
```

---

#### PATCH /api/v1/issues/{id}/mute

Mute the issue.

**Request body:**
```json
{"mute_mode": "until_regression"}
```

| Field | Values | Description |
|---|---|---|
| `mute_mode` | `until_regression`, `forever` | How to mute the issue. |

**Response:**
```json
{"issue": { ...updated issue object... }}
```

---

#### PATCH /api/v1/issues/{id}/unmute

Remove the mute and return the issue to `unresolved`. No request body.

**Response:**
```json
{"issue": { ...updated issue object... }}
```

---

### Events

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/events/{id}

Fetch a single event by ID.

**Response:**
```json
{"event": { ...event object... }}
```

---

#### GET /api/v1/events/stream

Server-Sent Events (SSE) stream of live events as they are processed from the spool.

See [SSE Endpoints](#sse-endpoints) for the stream format.

---

#### GET /api/v1/live/events

List recent events (polling alternative to SSE).

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `limit` | 50 | Maximum number of events to return. |
| `since` | — | RFC 3339 timestamp. Return only events received after this time. |

**Response:**
```json
{"events": [ ...event objects... ]}
```

---

### Releases

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/releases

List all releases.

**Response:**
```json
{"releases": [ ...release objects... ]}
```

---

#### POST /api/v1/releases

Create a release.

**Request body:**
```json
{
  "name": "v1.2.3",
  "environment": "production",
  "version": "1.2.3",
  "commit_sha": "abc123",
  "url": "https://github.com/org/repo/releases/tag/v1.2.3",
  "notes": "Bug fixes and performance improvements."
}
```

**Response:**
```json
{"release": { ...release object... }}
```

---

#### GET /api/v1/releases/{id}

Fetch a single release.

---

#### PATCH /api/v1/releases/{id}

Update a release. Accepts the same fields as the create request (all optional).

---

#### DELETE /api/v1/releases/{id}

Delete a release.

**Response:**
```json
{"deleted": true}
```

---

### Alerts

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/alerts

List all alerts.

**Response:**
```json
{"alerts": [ ...alert objects... ]}
```

---

#### POST /api/v1/alerts

Create an alert.

**Request body:**
```json
{
  "name": "High error rate",
  "enabled": true,
  "severity": "error",
  "condition": "event_count_exceeds",
  "threshold": 10,
  "cooldown_minutes": 60,
  "webhook_url": "https://hooks.example.com/notify"
}
```

**Response:**
```json
{"alert": { ...alert object... }}
```

---

#### GET /api/v1/alerts/{id}

Fetch a single alert.

---

#### PATCH /api/v1/alerts/{id}

Update an alert. Accepts the same fields as the create request (all optional).

---

#### DELETE /api/v1/alerts/{id}

Delete an alert.

**Response:**
```json
{"deleted": true}
```

---

### Logs

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/logs

Query stored log entries.

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `level` | — | Minimum log level name to return: `trace`, `debug`, `info`, `warn`, `error`, `fatal`. |
| `q` | — | Full-text search query against log messages. |
| `limit` | 200 | Maximum entries to return (capped at 500). |
| `before` | — | Return entries with ID less than this value (pagination cursor). |

**Response:**
```json
{
  "logs": [ ...log entry objects... ],
  "next_cursor": 12345
}
```

Pass `next_cursor` as `before` in the next request to page backwards through older entries.

---

#### GET /api/v1/logs/stream

Server-Sent Events (SSE) stream of live log entries as they are ingested.

See [SSE Endpoints](#sse-endpoints) for the stream format.

---

### Facets

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/facets

List all known facet keys.

**Response:**
```json
{"keys": ["environment", "severity", "release", "http.route"]}
```

---

#### GET /api/v1/facets/{key}

List all observed values for a given facet key.

**Response:**
```json
{"key": "environment", "values": ["production", "staging", "development"]}
```

---

### Projects

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/projects

List all projects.

**Response:**
```json
{"projects": [{"id": 1, "name": "My App", "slug": "my-app"}]}
```

---

#### POST /api/v1/projects

Create a project.

**Request body:**
```json
{"name": "My App", "slug": "my-app"}
```

**Response:**
```json
{"project": {"id": 1, "name": "My App", "slug": "my-app"}}
```

---

### API Keys

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/apikeys

List all API keys. The plaintext key value is never returned; only metadata is shown.

**Response:**
```json
{
  "apiKeys": [
    {
      "id": 1,
      "name": "production-sdk",
      "projectId": 1,
      "scope": "ingest",
      "createdAt": "2026-01-01T00:00:00Z",
      "lastUsedAt": "2026-04-26T10:00:00Z"
    }
  ]
}
```

---

#### POST /api/v1/apikeys

Create a new API key.

**Request body:**
```json
{"name": "my-key", "scope": "ingest", "project": "my-app"}
```

**Response:**
```json
{
  "apiKey": {
    "id": 2,
    "name": "my-key",
    "scope": "ingest",
    "projectId": 1,
    "key": "a3f1..."
  }
}
```

> The `key` field is only present in the creation response. Store it securely — it cannot be retrieved again.

---

#### DELETE /api/v1/apikeys/{id}

Delete an API key by its numeric ID.

**Response:**
```json
{"deleted": true}
```

---

### Settings

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/settings

Return current instance settings as a key-value map.

**Response:**
```json
{"settings": {"some_key": "some_value"}}
```

---

#### PUT /api/v1/settings

Update one or more settings.

**Request body:**
```json
{"some_key": "new_value"}
```

**Response:**
```json
{"settings": {"some_key": "new_value"}}
```

---

### Source Maps

**Auth:** Session cookie or full-scope API key.

#### GET /api/v1/source-maps

List uploaded source maps.

**Response:**
```json
{"sourceMaps": [ ...source map objects... ]}
```

---

#### POST /api/v1/source-maps

Upload a source map file. Uses `multipart/form-data`.

**Form fields:**

| Field | Description |
|---|---|
| `bundle_url` | URL of the JavaScript bundle this map corresponds to. |
| `release` | Release name or version string. |
| `dist` | Optional distribution identifier. |
| `source_map` | The source map file (`.map`). |

---

#### DELETE /api/v1/source-maps/{id}

Delete a source map by ID.

**Response:**
```json
{"deleted": true}
```

---

## SSE Endpoints

Two endpoints stream data using [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events):

- `GET /api/v1/events/stream` — live error events
- `GET /api/v1/logs/stream` — live log entries

### Stream Format

Each message is a standard SSE frame with a `data` field containing a JSON-encoded object:

```
data: {"id":1,"message":"Database connection failed","level":"error",...}

data: {"id":2,"message":"Order placed","level":"info",...}

```

Keep-alive comments are sent every 15 seconds to prevent proxy timeouts:

```
:

```

### Reconnection

The browser's `EventSource` API reconnects automatically on disconnect. To avoid re-displaying events already seen, track the last received event ID and use the `since` parameter on `GET /api/v1/live/events` (the polling alternative) when reconnecting.

### Project Filtering

SSE streams respect the `X-BugBarn-Project` header. Omitting the header streams events from all projects (session auth only).

---

## Event Payload

The full shape of the JSON body for `POST /api/v1/events`:

```json
{
  "timestamp": "2026-04-26T10:30:00.000Z",
  "severityText": "error",
  "body": "Something went wrong",
  "exception": {
    "type": "TypeError",
    "message": "Cannot read property 'x' of undefined",
    "stacktrace": [
      {
        "function": "processOrder",
        "module": "orders",
        "filename": "orders.js",
        "lineno": 42
      }
    ]
  },
  "attributes": {
    "http.route": "/api/orders",
    "http.method": "POST",
    "http.status_code": "500",
    "environment": "production",
    "release": "v1.2.3"
  },
  "resource": {
    "service.name": "api",
    "host.name": "web-01"
  },
  "user": {
    "id": "usr_123",
    "email": "user@example.com",
    "username": "johndoe"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `timestamp` | RFC 3339 string | When the event occurred. |
| `severityText` | string | Severity level: `trace`, `debug`, `info`, `warn`, `error`, `fatal`. |
| `body` | string | Human-readable event message. |
| `exception` | object | Optional. Exception details. |
| `exception.type` | string | Exception class name. |
| `exception.message` | string | Exception message. |
| `exception.stacktrace` | array | Stack frames. Each frame may include `function`, `module`, `filename`, `lineno`, `colno`. |
| `attributes` | object | Arbitrary key-value metadata. Values used for faceting and filtering. |
| `resource` | object | Resource attributes describing the originating service or host. |
| `user` | object | Optional. User context: `id`, `email`, `username`. |

All top-level fields except `body` are optional. Ingest accepts any JSON object — unrecognised fields are stored and surfaced in the UI.

---

## Log Entry Payload

The shape of each entry in `POST /api/v1/logs`:

```json
{
  "timestamp": "2026-04-26T10:30:00Z",
  "level": "error",
  "level_num": 50,
  "message": "Database connection failed",
  "data": {
    "host": "db.internal",
    "retries": 3
  }
}
```

| Field | Type | Description |
|---|---|---|
| `timestamp` | RFC 3339 string | When the log entry was produced. |
| `level` | string | Level name: `trace`, `debug`, `info`, `warn`, `error`, `fatal`. |
| `level_num` | integer | Pino-style numeric level (10, 20, 30, 40, 50, 60). Used when `level` is absent or for numeric-only loggers. |
| `message` | string | Log message text. Also accepted under the key `msg` (Pino convention). |
| `data` | object | Arbitrary additional structured fields. |

The log ingest endpoint also accepts raw [Pino](https://getpino.io) JSON output (where `msg` is the message key and `time` is epoch milliseconds) — both via `application/json` with a `{"logs":[...]}` wrapper and via `application/x-ndjson` line-by-line.
