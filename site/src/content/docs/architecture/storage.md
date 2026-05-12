# BugBarn — Storage

## SQLite Setup

BugBarn opens a single SQLite file (default `.data/bugbarn.db`) using the `modernc.org/sqlite` pure-Go driver. Three PRAGMAs are applied on every connection:

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
```

| PRAGMA | Value | Reason |
|---|---|---|
| `foreign_keys` | `ON` | Enforces referential integrity declared in the schema (cascade deletes, key constraints). SQLite disables foreign keys by default. |
| `journal_mode` | `WAL` | Write-Ahead Logging allows API-server reads to proceed concurrently with background-worker writes without blocking. Essential because BugBarn has a continuous write path (background worker) and a continuous read path (API server). |
| `synchronous` | `NORMAL` | SQLite syncs at WAL checkpoints rather than after every individual write. This improves write throughput at the cost of a small durability window: data written since the last checkpoint could be lost in an OS crash (not a power failure, which WAL already protects against). Acceptable for a self-hosted error tracker where a few seconds of data loss during a crash is tolerable. |

The connection pool is capped at one open connection (`db.SetMaxOpenConns(1)`) to serialise all writes through a single SQLite file handle.

---

## Schema

### `projects`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `slug` | `TEXT UNIQUE` | URL-safe identifier used as routing key during ingest (`x-bugbarn-project` header) |
| `name` | `TEXT` | Human-readable display name |
| `created_at` | `TEXT` | ISO 8601 timestamp |

A `default` project is always created on first startup.

---

### `issues`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `fingerprint` | `TEXT` | SHA-256 hex digest that identifies this logical issue; unique per project |
| `fingerprint_material` | `TEXT` | The JSON string that was hashed (for debugging) |
| `fingerprint_explanation_json` | `TEXT` | JSON array of human-readable strings explaining which fields contributed to the fingerprint |
| `title` | `TEXT` | Issue title derived from the first event |
| `normalized_title` | `TEXT` | Lower-cased, whitespace-collapsed title used for deduplication display |
| `exception_type` | `TEXT` | Normalised exception type from the first event |
| `status` | `TEXT` | `unresolved`, `resolved`, or `ignored` |
| `mute_mode` | `TEXT` | Optional mute behaviour (empty = not muted) |
| `resolved_at` | `TEXT` | ISO 8601 timestamp when last resolved (empty if never) |
| `reopened_at` | `TEXT` | ISO 8601 timestamp when last reopened |
| `last_regressed_at` | `TEXT` | ISO 8601 timestamp of the most recent regression |
| `regression_count` | `INTEGER` | Total number of regressions |
| `first_seen` | `TEXT` | ISO 8601 timestamp of the first event |
| `last_seen` | `TEXT` | ISO 8601 timestamp of the most recent event |
| `event_count` | `INTEGER` | Total number of events grouped under this issue |
| `representative_event_json` | `TEXT` | Full JSON of the most recent event, stored for quick display without a join |
| `created_at` | `TEXT` | Row creation timestamp |
| `updated_at` | `TEXT` | Row last-update timestamp |

**Unique constraint:** `(project_id, fingerprint)` — one issue per fingerprint per project.

---

### `events`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `issue_id` | `INTEGER` | Foreign key → `issues(id)` ON DELETE CASCADE |
| `fingerprint` | `TEXT` | Fingerprint of this event (matches parent issue) |
| `fingerprint_material` | `TEXT` | JSON material that produced the fingerprint |
| `fingerprint_explanation_json` | `TEXT` | Human-readable explanation of the fingerprint |
| `received_at` | `TEXT` | When the ingest handler accepted the HTTP request |
| `observed_at` | `TEXT` | Timestamp from the event payload itself |
| `severity` | `TEXT` | Severity string from the event |
| `message` | `TEXT` | Human-readable message |
| `regressed` | `INTEGER` | `1` if this event caused a regression (issue re-opened after resolve); `0` otherwise |
| `event_json` | `TEXT` | Full normalised event payload as JSON |
| `user_json` | `TEXT` | User context extracted from the event |
| `breadcrumbs_json` | `TEXT` | Breadcrumbs extracted from the event |
| `created_at` | `TEXT` | Row creation timestamp |

---

### `event_facets`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `event_id` | `INTEGER` | Foreign key → `events(id)` ON DELETE CASCADE |
| `issue_id` | `INTEGER` | Foreign key → `issues(id)` ON DELETE CASCADE |
| `section` | `TEXT` | Grouping label derived from the key prefix (e.g. `http`, `service`) |
| `facet_key` | `TEXT` | Attribute name (e.g. `http.route`, `severity`) |
| `facet_value` | `TEXT` | Attribute value |

See [Event Facets](#event-facets) below for cardinality caps and the list of extracted keys.

---

### `releases`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `name` | `TEXT` | Release name |
| `environment` | `TEXT` | Environment label |
| `observed_at` | `TEXT` | When the release was first observed |
| `version` | `TEXT` | Version string |
| `commit_sha` | `TEXT` | Git commit SHA |
| `url` | `TEXT` | URL to release (e.g. GitHub tag) |
| `notes` | `TEXT` | Release notes |
| `created_by` | `TEXT` | Who triggered the release |
| `created_at` | `TEXT` | Row creation timestamp |

---

### `alerts`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `name` | `TEXT` | Human-readable alert name |
| `enabled` | `INTEGER` | `1` = active; `0` = disabled |
| `severity` | `TEXT` | Severity filter (may be empty = any severity) |
| `rule_json` | `TEXT` | Reserved structured rule JSON |
| `webhook_url` | `TEXT` | HTTP endpoint to POST when the alert fires |
| `condition` | `TEXT` | `new_issue`, `regression`, or `event_count_exceeds` |
| `threshold` | `INTEGER` | For `event_count_exceeds`: fires when `event_count > threshold` |
| `cooldown_minutes` | `INTEGER` | Minimum minutes between firings for the same alert/issue pair |
| `last_fired_at` | `TEXT` | ISO 8601 timestamp of the most recent firing (any issue) |
| `created_at` | `TEXT` | Row creation timestamp |
| `updated_at` | `TEXT` | Row last-update timestamp |

---

### `alert_firings`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `alert_id` | `INTEGER` | References `alerts(id)` |
| `issue_id` | `INTEGER` | References `issues(id)` |
| `fired_at` | `TEXT` | ISO 8601 timestamp of the firing |

Used to enforce per-alert, per-issue cooldowns.

---

### `settings`

| Column | Type | Description |
|---|---|---|
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `key` | `TEXT` | Setting name |
| `value` | `TEXT` | Setting value |
| `updated_at` | `TEXT` | Last-update timestamp |

**Primary key:** `(project_id, key)`.

---

### `source_maps`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Foreign key → `projects(id)` ON DELETE CASCADE |
| `release` | `TEXT` | Release string the source map applies to |
| `dist` | `TEXT` | Distribution identifier (may be empty) |
| `bundle_url` | `TEXT` | URL of the minified bundle this map corresponds to |
| `name` | `TEXT` | File name |
| `content_type` | `TEXT` | MIME type |
| `source_map_blob` | `BLOB` | Raw source map bytes |
| `size_bytes` | `INTEGER` | Size of the blob |
| `created_at` | `TEXT` | Upload timestamp |

---

### `users`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `username` | `TEXT UNIQUE` | Login username |
| `password_bcrypt` | `TEXT` | bcrypt hash of the password |
| `created_at` | `TEXT` | Row creation timestamp |
| `updated_at` | `TEXT` | Row last-update timestamp |

---

### `api_keys`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `name` | `TEXT` | Human-readable label |
| `project_id` | `INTEGER` | Foreign key → `projects(id)`. Requests authenticated with this key are scoped to this project |
| `key_sha256` | `TEXT UNIQUE` | SHA-256 hex digest of the plaintext key. The plaintext is never stored |
| `scope` | `TEXT` | `full` (all endpoints) or `ingest` (`POST /api/v1/events` and `POST /api/v1/logs` only) |
| `created_at` | `TEXT` | Row creation timestamp |
| `last_used_at` | `TEXT` | Updated on every successful authentication |

---

### `log_entries`

| Column | Type | Description |
|---|---|---|
| `id` | `INTEGER PK` | Auto-increment primary key |
| `project_id` | `INTEGER` | Project that owns this log entry |
| `received_at` | `TEXT` | ISO 8601 timestamp when BugBarn received the entry |
| `level_num` | `INTEGER` | Numeric log level (default 30 = info) |
| `level` | `TEXT` | Level string (e.g. `info`, `warn`, `error`) |
| `message` | `TEXT` | Log message |
| `data_json` | `TEXT` | Arbitrary structured data as JSON |

---

## Fingerprinting Algorithm

Fingerprinting groups distinct events into the same logical issue. Two events share a fingerprint if and only if their normalised exception type, message, stacktrace, and stable context produce the same SHA-256 digest.

### Input Material

The fingerprinter builds a `material` struct:

```go
type material struct {
    ExceptionType string            `json:"exceptionType,omitempty"`
    Message       string            `json:"message,omitempty"`
    Stacktrace    []string          `json:"stacktrace,omitempty"`
    Context       map[string]string `json:"context,omitempty"`
}
```

- **`ExceptionType`** — `normalize(event.Exception.Type)`
- **`Message`** — `normalize(event.Exception.Message)`, falling back to `normalize(event.Message)` when the exception message is empty
- **`Stacktrace`** — for each frame: `normalize(frame.Module) + ":" + normalize(frame.Function) + ":" + normalizePath(frame.File)`; empty parts are omitted; frames that produce an empty string are skipped
- **`Context`** — the subset of fields from `event.Resource` and `event.Attributes` whose keys appear in the stable-context allowlist (see below)

### Stable Context Allowlist

Only these keys are included in the context map, preventing high-cardinality values (e.g. request IDs) from causing fingerprint drift:

```
environment, host, http.method, http.route, http.status_code,
region, release, route, service.name, service.namespace,
status_code, user_agent.family, version
```

Key matching is done after normalisation and also accepts suffix matches (e.g. `foo.environment` matches `environment`).

### Normalisation Steps

Normalisation is applied to every string before it contributes to the fingerprint material:

1. Lowercase and trim whitespace
2. UUIDs → `<id>`
3. IPv4 addresses → `<ip>`
4. Hex addresses (`0x` followed by 6+ hex digits) → `<hex>`
5. Long numbers (4+ consecutive digits as a word) → `<num>`
6. Privacy-scrubber placeholders (`[redacted-id]`, `[redacted-ip]`, `[redacted-email]`, `[redacted-secret]`) → `<redacted>`
7. Collapse runs of whitespace to a single space

Path normalisation additionally:
- Replaces backslashes with forward slashes
- Replaces numeric path segments (`/123`) with `/:num`

### Digest

```
fingerprint = hex( SHA-256( JSON.Marshal(material) ) )
```

The JSON serialisation is deterministic because map keys are iterated in sorted order by the Go JSON encoder and by the fingerprinter's explicit key sorting.

The raw JSON material string and a human-readable explanation array are also stored alongside the fingerprint in `issues.fingerprint_material` and `issues.fingerprint_explanation_json` for debugging.

---

## Event Facets

Facets are a flat set of key/value pairs extracted from each event and stored in the `event_facets` table. They enable the API and UI to offer filtered views of issues (e.g. "show all issues where `http.route = /api/v1/users`") without requiring a full-text scan of the `event_json` column.

### Extracted Keys

The following fields are extracted from each event:

**From `event.Resource`:**
- `host.name`
- `service.name`
- `telemetry.sdk.language`
- `deployment.environment`

**From `event.Attributes`:**
- `http.route`
- `http.status_code`
- `http.method`
- `user_agent.original`

**Derived:**
- `severity` — from the event severity field
- `environment` — attributes value preferred over resource value
- `release` — from attributes

### Cardinality Caps

To prevent unbounded table growth, two hard limits are enforced per project within a transaction on every ingest:

| Cap | Limit |
|---|---|
| Distinct facet keys per project | **50** |
| Distinct values per facet key per project | **10 000** |

If either limit is reached, the facet entry is silently skipped. The event itself is still persisted in full; only the facet index entry is omitted.

---

## The Event Spool

### Purpose

The spool decouples HTTP ingest from SQLite writes. The ingest handler never touches the database; it simply appends a record to a file and returns `202 Accepted`. The background worker reads from the spool independently.

### File Format

Records are stored as append-only newline-delimited JSON (NDJSON) in `.data/spool/ingest.ndjson`. Each line is one serialised `spool.Record`:

```json
{"ingestId":"a1b2c3d4e5f6","receivedAt":"2026-04-26T10:00:00Z","contentType":"application/json","remoteAddr":"10.0.0.1:54321","contentLength":512,"bodyBase64":"eyJ...","projectSlug":"my-app"}
```

| Field | Description |
|---|---|
| `ingestId` | 24-hex-character random ID generated at ingest time |
| `receivedAt` | UTC timestamp when the HTTP request was accepted |
| `contentType` | `Content-Type` header from the request |
| `remoteAddr` | Client IP and port |
| `contentLength` | Body size in bytes |
| `bodyBase64` | Base64-encoded raw request body |
| `projectSlug` | Value of the `X-BugBarn-Project` header, if present |

### Cursor

`.data/spool/cursor.json` stores a single byte offset:

```json
{"offset": 1048576}
```

On startup the background worker reads this file to resume from where it left off. After each successfully processed record the worker writes the new offset to `cursor.json`. This ensures that a crash between processing and writing the cursor causes at-most-once re-processing of the last record rather than silent loss.

### Rotation

When the active spool file exceeds **64 MiB**, the worker renames `ingest.ndjson` to `ingest-YYYYMMDDTHHMMSSZ.ndjson` (UTC timestamp) and opens a fresh `ingest.ndjson`. The cursor is not reset on rotation; old rotated segments are left in the spool directory for manual archival or deletion.

### Dead-Letter File

If a record fails processing (decode, normalise, or persist) **3 times**, it is written to `.data/spool/deadletter.ndjson` and the cursor advances past it. The dead-letter file preserves the original `spool.Record` in full so that the record can be inspected and replayed manually.

### Back-Pressure (429)

The ingest handler maintains a 32 768-record in-memory channel between the HTTP goroutines and the spool-flusher goroutine. If that channel is full when a new request arrives, the handler responds with:

```
HTTP 429 Too Many Requests
Retry-After: 1
```

The `BUGBARN_MAX_SPOOL_BYTES` environment variable sets an additional byte limit on the spool file itself. If adding a batch would exceed this limit, `spool.AppendBatch` returns `spool.ErrFull`.

---

## Litestream Replication

BugBarn does not bundle any replication logic. Continuous off-site replication is handled by [Litestream](https://litestream.io/), a separate binary that streams SQLite WAL frames to an object store (S3, GCS, Azure Blob, SFTP, etc.) in near-real time.

Litestream is configured entirely via its own configuration file or environment variables. BugBarn has no knowledge of it; BugBarn simply operates on the SQLite file as normal. Common Litestream environment variables used alongside BugBarn deployments:

| Variable | Purpose |
|---|---|
| `LITESTREAM_ACCESS_KEY_ID` | S3-compatible access key |
| `LITESTREAM_SECRET_ACCESS_KEY` | S3-compatible secret key |
| `LITESTREAM_REPLICA_URL` | Replica destination (e.g. `s3://bucket/bugbarn.db`) |

See the [Litestream documentation](https://litestream.io/reference/config/) for the full reference.

---

## Index Strategy

| Index | Table | Columns | Purpose |
|---|---|---|---|
| `idx_issues_project_last_seen` | `issues` | `(project_id, last_seen DESC, id DESC)` | Powers the default issues list, ordered by most recently active within a project |
| `idx_events_issue_id` | `events` | `(project_id, issue_id, id ASC)` | Fetches the event history for a single issue in chronological order |
| `idx_events_project_received_at` | `events` | `(project_id, received_at DESC, id DESC)` | Powers the recent-events feed ordered by ingest time within a project |
| `idx_releases_project_observed_at` | `releases` | `(project_id, observed_at DESC, id DESC)` | Lists releases for a project ordered by most recent |
| `idx_event_facets_lookup` | `event_facets` | `(project_id, section, facet_key, facet_value)` | Supports facet key/value queries and cardinality cap checks |
| `idx_alert_firings_lookup` | `alert_firings` | `(alert_id, issue_id, fired_at DESC)` | Cooldown check: most recent firing for a given alert/issue pair |
| `idx_log_entries_project_id` | `log_entries` | `(project_id, id DESC)` | Paginates log entries for a project in reverse-insertion order |
