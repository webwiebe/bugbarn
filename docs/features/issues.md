# Issue Tracking

BugBarn groups incoming error events into issues using deterministic fingerprinting. Multiple occurrences of the same error produce a single issue with a running event count rather than separate rows.

---

## How Events Become Issues

When an event is ingested, BugBarn computes a fingerprint and looks up an existing issue with that fingerprint for the same project. If one exists, the event is appended to it and the issue counters are updated. If none exists, a new issue is created with status `unresolved`.

### Fingerprinting

The fingerprint is a SHA-256 hex digest of a JSON document built from four components:

| Component | Source |
|-----------|--------|
| `exceptionType` | `exception.type`, normalised |
| `message` | `exception.message` (falls back to top-level `message`), normalised |
| `stacktrace` | Each frame rendered as `module:function:file`, normalised |
| `context` | Stable context key/value pairs extracted from `resource` and `attributes` |

**Normalisation** strips noise that would cause identical errors to produce different fingerprints:

- UUIDs → `<id>`
- IPv4 addresses → `<ip>`
- Hex addresses (`0x…` ≥ 6 digits) → `<hex>`
- Numbers ≥ 4 digits → `<num>`
- Path segments that are pure numbers → `/:num`
- `[redacted-id]`, `[redacted-ip]`, `[redacted-email]`, `[redacted-secret]` → `<redacted>`
- Consecutive whitespace is collapsed
- All text is lower-cased

**Stable context keys** included in the fingerprint (matched by suffix):

```
environment   host              http.method      http.route
http.status_code  region        release          route
service.name  service.namespace  status_code     user_agent.family
version
```

Keys not in this list are excluded from the fingerprint even when present on the event.

For further detail on storage layout, see [`docs/architecture`](../architecture.md).

---

## Issue Lifecycle

```
                   new event arrives
                   ┌─────────────────────────────────────────────┐
                   │                                             │
               ┌───┴──────┐   resolve    ┌──────────┐           │
  new issue ──▶│unresolved│─────────────▶│ resolved │           │
               └───┬──────┘              └────┬─────┘           │
                   │ mute                     │ new event        │
                   ▼                          │ (regression)     │
               ┌──────────┐◀─────────────────┘                  │
               │  muted   │                                      │
               └────┬─────┘                                      │
                    │ unmute                                      │
                    ▼                                             │
               ┌──────────┐   resolve    ┌──────────┐           │
               │unresolved│─────────────▶│ resolved │───────────┘
               └──────────┘              └──────────┘
                                              │ new event (regression)
                                              ▼
                                        ┌──────────┐
                                        │regressed │
                                        └──────────┘
```

### Status Descriptions

| Status | Meaning |
|--------|---------|
| `unresolved` | Active issue; shows in the default open view |
| `resolved` | Manually resolved; hidden from the open view |
| `regressed` | Was resolved but a new event arrived; treated as open |
| `muted` | Suppressed; excluded from the open view |

---

## Mute Modes

Muting is a way to silence an issue without resolving it. There are two modes:

### `until_regression`

The issue stays muted until a new event arrives. On the first new event, the mute is automatically cleared and the issue transitions to `regressed`. Use this for intermittent issues you want to be notified about if they resurface.

### `forever`

The issue stays muted regardless of new events. New events still increment `event_count` and update `last_seen`, but the status never changes. Use this for known, intentional noise you never want to act on.

To remove either mute mode and return the issue to `unresolved`, send a PATCH to the unmute endpoint.

---

## Regression Detection

A regression occurs when a new event arrives for an issue that is in `resolved` status, or for an issue that is `muted` with mode `until_regression`.

When a regression is detected, the following fields are updated atomically:

| Field | Change |
|-------|--------|
| `status` | → `regressed` (or `unresolved` if unmuting) |
| `mute_mode` | Cleared to `""` when `until_regression` unmutes |
| `regression_count` | Incremented by 1 |
| `last_regressed_at` | Set to the event's observed/received timestamp |
| `reopened_at` | Set to the same timestamp |

Issues with `mute_mode: "forever"` are never regressed; new events are counted silently.

---

## Browsing Issues

### Status Filter

The `status` query parameter controls which issues are returned:

| Value | Issues returned |
|-------|----------------|
| `open` (default) | `unresolved` and `regressed` |
| `muted` | `muted` only |
| `resolved` | `resolved` only |
| `all` | No status filter; all issues returned |

### Sort Order

The `sort` query parameter controls ordering. All sorts are descending.

| Value | Sorts by |
|-------|----------|
| `last_seen` (default) | Most recently seen event |
| `first_seen` | Oldest first occurrence |
| `event_count` | Total number of events |

### Full-Text Search

The `q` parameter performs a case-insensitive substring match against both the raw `title` and a pre-computed `normalized_title` (which has UUIDs, IPs, and numbers replaced with placeholders). This allows a search for `user 123` to match issues whose normalised title contains `user <num>`.

---

## Faceted Filtering

Facets are indexed key/value pairs extracted from event fields at ingest time. They let you slice the issue list by operational dimensions such as environment, host, or HTTP route.

### Available Facet Keys

| Key | Event source |
|-----|-------------|
| `severity` | `event.severity` |
| `environment` | `attributes.deployment.environment` or `resource.deployment.environment` |
| `host.name` | `resource.host.name` |
| `service.name` | `resource.service.name` |
| `telemetry.sdk.language` | `resource.telemetry.sdk.language` |
| `deployment.environment` | `resource.deployment.environment` |
| `http.route` | `attributes.http.route` |
| `http.status_code` | `attributes.http.status_code` |
| `http.method` | `attributes.http.method` |
| `user_agent.original` | `attributes.user_agent.original` |
| `release` | `attributes.release` |

### Cardinality Limits

To keep the index bounded, BugBarn enforces:

- **50 distinct facet keys** per project. Keys seen after the cap is reached are silently ignored.
- **10,000 distinct values** per key per project. Values beyond this are silently ignored.

### Using Facets in Queries

Pass one or more `facets` parameters as `key:value` pairs. Multiple facets are combined with AND semantics — an issue must match all supplied facets to appear in the result.

```
GET /api/v1/issues?facets=environment:production&facets=http.method:POST
```

---

## Issue Detail

Each issue stores:

- **`title`** — `ExceptionType: message`, or just one of those if the other is absent
- **`status`** / **`mute_mode`**
- **`first_seen`** / **`last_seen`** / **`event_count`**
- **`regression_count`** / **`last_regressed_at`**
- **`fingerprint`** — the hex SHA-256 used for grouping
- **`fingerprint_material`** — the raw JSON document that was hashed (useful for debugging grouping decisions)
- **`fingerprint_explanation`** — ordered list of `component=value` strings that went into the hash
- **`representative_event`** — the full payload of the **most recent** event, stored as JSON. This is what is displayed on the issue detail page.

### Event History

`GET /api/v1/issues/{id}/events` returns individual event records for an issue, paginated. Each event record contains:

- `received_at` / `observed_at`
- `severity`
- `regressed` flag (whether this event triggered a regression)
- The full original event payload including exception, stacktrace, breadcrumbs, user context, and arbitrary attributes/resource fields

---

## Issue Actions

### Resolve

```http
POST /api/v1/issues/{id}/resolve
```

Transitions the issue to `resolved`. No request body. New events will trigger a regression.

### Reopen

```http
POST /api/v1/issues/{id}/reopen
```

Transitions the issue back to `unresolved`. No request body. Also clears any active mute.

### Mute (until regression)

```http
PATCH /api/v1/issues/{id}/mute
Content-Type: application/json

{"mute_mode": "until_regression"}
```

### Mute (forever)

```http
PATCH /api/v1/issues/{id}/mute
Content-Type: application/json

{"mute_mode": "forever"}
```

### Unmute

```http
PATCH /api/v1/issues/{id}/unmute
```

Removes the mute and returns the issue to `unresolved`. No request body.

---

## API Reference

### `GET /api/v1/issues`

List issues for the current project (or all projects in all-projects mode).

**Query parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `status` | string | `open` \| `muted` \| `resolved` \| `all`. Default: `open` |
| `sort` | string | `last_seen` \| `first_seen` \| `event_count`. Default: `last_seen` |
| `q` | string | Substring search on title |
| `facets` | string (repeatable) | `key:value` pairs; AND semantics |

**Response**

```json
[
  {
    "id": "issue-000001",
    "title": "TypeError: Cannot read properties of undefined",
    "status": "unresolved",
    "mute_mode": "",
    "first_seen": "2026-04-20T08:00:00Z",
    "last_seen": "2026-04-26T10:00:00Z",
    "event_count": 42,
    "regression_count": 1,
    "last_regressed_at": "2026-04-25T12:00:00Z",
    "project_slug": "my-app"
  }
]
```

`project_slug` is included when operating in all-projects mode.

---

### `GET /api/v1/issues/{id}`

Return a single issue by ID. Includes `representative_event` and `fingerprint_explanation`.

---

### `GET /api/v1/issues/{id}/events`

List individual events belonging to an issue.

---

### `POST /api/v1/issues/{id}/resolve`

Mark the issue as resolved. No request body.

---

### `POST /api/v1/issues/{id}/reopen`

Reopen the issue to `unresolved`. No request body.

---

### `PATCH /api/v1/issues/{id}/mute`

Mute the issue.

**Request body**

| Field | Type | Values |
|-------|------|--------|
| `mute_mode` | string | `"until_regression"` \| `"forever"` |

---

### `PATCH /api/v1/issues/{id}/unmute`

Remove the mute and return the issue to `unresolved`. No request body.
