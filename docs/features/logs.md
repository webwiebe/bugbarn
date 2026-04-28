# Log Ingestion & Streaming

BugBarn provides structured log aggregation alongside error tracking. Logs are stored per project and can be queried historically or streamed in real time via Server-Sent Events (SSE).

---

## Purpose

Log ingestion lets you ship application log lines to BugBarn so that they appear alongside issue data in the same tool. This is useful for correlating a spike in errors with the log output from the same time window, without switching to a separate logging platform.

---

## Ingest Endpoint

### `POST /api/v1/logs`

Accepts one or more structured log entries and stores them for the project.

#### Authentication

The endpoint accepts:

- An API key with **full** or **ingest** scope (via `X-BugBarn-Api-Key` header)
- A valid session cookie (browser UI)

The project is resolved from the API key's bound project, or from the `X-BugBarn-Project` header. A project header with an unknown slug auto-creates the project.

#### Content Types

The endpoint accepts two content types:

**`application/json`** (default)

```http
POST /api/v1/logs
Content-Type: application/json
X-BugBarn-Api-Key: <key>
X-BugBarn-Project: my-app

{
  "logs": [
    {
      "level": 30,
      "time": 1745662200000,
      "msg": "user logged in",
      "user_id": 123,
      "ip": "10.0.0.1"
    }
  ]
}
```

**`application/x-ndjson`** — one JSON object per line, useful for bulk shipping:

```http
POST /api/v1/logs
Content-Type: application/x-ndjson
X-BugBarn-Api-Key: <key>
X-BugBarn-Project: my-app

{"level":30,"time":1745662200000,"msg":"user logged in","user_id":123}
{"level":50,"time":1745662201000,"msg":"database error","err":"connection refused"}
```

#### Entry Fields

BugBarn parses [Pino](https://github.com/pinojs/pino)-style log objects. The following fields have special meaning:

| Field | Type | Description |
|-------|------|-------------|
| `msg` or `message` | string | The log message text |
| `level` | number or string | Level as a Pino numeric value or level name string |
| `time` | number | Unix timestamp in **milliseconds**; defaults to server receive time if absent |

All other fields are collected into the `data` object on the stored entry. The reserved fields `v`, `pid`, `hostname`, `msg`, `message`, `level`, and `time` are excluded from `data`.

#### Response

On success: `204 No Content`.

If the request body contains no parseable entries: `204 No Content` (no-op).

---

## Log Levels

BugBarn uses Pino numeric levels. Both numeric and string forms are accepted on ingest.

| Name | Numeric |
|------|---------|
| `trace` | 10 |
| `debug` | 20 |
| `info` | 30 |
| `warn` | 40 |
| `error` | 50 |
| `fatal` | 60 |

When a numeric level that does not match a known name is received, the raw number is stored as the level string.

---

## Storage Limit

Each project retains the **10,000 most recent log entries**. When a batch insertion causes the count to exceed 10,000, older entries are trimmed in the same database transaction.

---

## Query API

### `GET /api/v1/logs`

Returns stored log entries, newest first.

**Query parameters**

| Parameter | Description |
|-----------|-------------|
| `level` | Minimum level name (e.g. `error`). Returns entries with `level_num >= ` the numeric equivalent. |
| `q` | Substring match on the `message` field (case-insensitive). |
| `limit` | Maximum entries to return. Default: `200`. Maximum: `500`. |
| `before` | Cursor: return only entries with `id < before`. Use `next_cursor` from a previous response for pagination. |

**Response**

```json
{
  "logs": [
    {
      "id": 9871,
      "project_id": 1,
      "project_slug": "my-app",
      "received_at": "2026-04-26T10:30:01Z",
      "level_num": 50,
      "level": "error",
      "message": "database error",
      "data": {"err": "connection refused"}
    }
  ],
  "next_cursor": 9871
}
```

`project_slug` is included when operating in all-projects mode (no `X-BugBarn-Project` header with session auth). Pass `next_cursor` as the `before` parameter in the next request to paginate backwards through older entries.

---

## Live Streaming (SSE)

### `GET /api/v1/logs/stream`

Opens a persistent [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events) connection. New log entries are pushed to the client as they are ingested.

**Response headers**

```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Accel-Buffering: no
```

**Event format**

Each log entry is sent as an SSE data frame:

```
data: {"id":9872,"project_id":1,"received_at":"2026-04-26T10:30:02Z","level_num":30,"level":"info","message":"user logged in","data":{"user_id":456}}

```

There is no named event type — clients should listen for the default `message` event.

**Keepalive**

A comment (`:\n\n`) is sent every **15 seconds** to keep the connection alive through proxies and load balancers that close idle connections.

### Connection Lifecycle

1. Client connects. The server registers a subscriber channel with a **64-entry buffer**.
2. New log entries ingested for the project are sent to the client immediately.
3. If the client is too slow to consume entries and the 64-entry buffer fills, **new entries are dropped** for that subscriber (non-blocking send). No error is signalled; the client simply misses those entries.
4. When the client disconnects, the channel is unregistered and closed.

---

## All-Projects Log View

When a session-authenticated request omits the `X-BugBarn-Project` header:

- `GET /api/v1/logs` returns log entries from **all projects**, each with a `project_slug` field.
- `GET /api/v1/logs/stream` subscribes to a special `projectID=0` channel in the hub that receives entries from **every project** as they are ingested.

This makes it possible to tail logs across a whole fleet from a single SSE connection.

---

## Practical Examples

### Send a batch via curl (JSON)

```bash
curl -X POST https://bugbarn.example.com/api/v1/logs \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: <key>" \
  -H "X-BugBarn-Project: my-app" \
  -d '{
    "logs": [
      {"level": 30, "time": 1745662200000, "msg": "server started", "port": 3000},
      {"level": 50, "time": 1745662201000, "msg": "unhandled error", "err": "ECONNRESET"}
    ]
  }'
```

### Send NDJSON via curl

```bash
printf '{"level":30,"time":1745662200000,"msg":"ping"}\n{"level":40,"time":1745662201000,"msg":"slow query","ms":1200}\n' | \
  curl -X POST https://bugbarn.example.com/api/v1/logs \
    -H "Content-Type: application/x-ndjson" \
    -H "X-BugBarn-Api-Key: <key>" \
    -H "X-BugBarn-Project: my-app" \
    --data-binary @-
```

### Stream live logs via curl

```bash
curl -N \
  -H "Accept: text/event-stream" \
  -H "X-BugBarn-Api-Key: <key>" \
  -H "X-BugBarn-Project: my-app" \
  https://bugbarn.example.com/api/v1/logs/stream
```

### Query recent errors

```bash
curl "https://bugbarn.example.com/api/v1/logs?level=error&limit=50" \
  -H "X-BugBarn-Api-Key: <key>" \
  -H "X-BugBarn-Project: my-app"
```

### Pino transport

BugBarn accepts standard Pino output directly. You can pipe Pino logs to BugBarn using any HTTP-capable Pino transport. A minimal example using `pino-http-send`:

```js
// pino.config.js
export default {
  transport: {
    target: 'pino-http-send',
    options: {
      url: 'https://bugbarn.example.com/api/v1/logs',
      method: 'POST',
      contentType: 'application/x-ndjson',
      headers: {
        'X-BugBarn-Api-Key': process.env.BUGBARN_API_KEY,
        'X-BugBarn-Project': process.env.BUGBARN_PROJECT,
      },
    },
  },
}
```

Because BugBarn parses the Pino fields (`level`, `time`, `msg`) natively, no custom serialisers are needed.

---

## API Reference

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/logs` | Ingest one or more log entries |
| `GET` | `/api/v1/logs` | Query stored log entries |
| `GET` | `/api/v1/logs/stream` | Stream live entries via SSE |
