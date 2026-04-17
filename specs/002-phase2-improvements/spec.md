# Feature Specification: Phase 2 & 3 Improvements

**Feature Branch**: `002-phase2-improvements`  
**Created**: 2026-04-18  
**Status**: Draft  
**Input**: Gap analysis between BugBarn and Sentry identifying the highest-leverage improvements for a personal error tracker. Phase 2 targets backend-only changes with immediate operational value; Phase 3 adds user context and breadcrumbs requiring SDK changes across all languages.

---

## Background

BugBarn's foundation (Phase 1) delivers ingest, deduplication, a web UI, SSE live events, release markers, source map symbolication, and TypeScript/Python/PHP SDKs. The remaining gaps that hurt daily use are:

1. **Alerts don't fire** — CRUD exists but no delivery mechanism.
2. **Issues can't be silenced** — only resolved/unresolved; known noise keeps resurfacing.
3. **No frequency signal** — total count and last-seen but no spike detection.
4. **Errors are anonymous** — no user identity on events.
5. **Crashes lack context** — no breadcrumb trail before the exception.
6. **BugBarn is not monitored by BugBarn** — the service reports errors only to stderr logs.

---

## Phase 2

### User Story P2-1 — Operators Are Notified When Issues Spike or Appear

As a homelab operator, I want BugBarn to post to a Slack, Discord, or custom webhook when a new issue appears or a resolved issue regresses, so I know immediately without polling the web UI.

**Why this priority**: This is the single biggest gap. All the alert rule UI is wired up but nothing is ever delivered. Without delivery, BugBarn is a log viewer, not a monitoring tool.

**Independent Test**: Create an alert rule with a webhook URL. Send events matching the rule. Verify an HTTP POST is received at the target URL within 30 seconds, with the correct payload shape, and that duplicate firing is suppressed until the cooldown expires.

**Acceptance Scenarios**:

1. **Given** an enabled alert rule with a `webhook_url` and a `condition` of `new_issue`, **When** the worker persists a new issue, **Then** BugBarn sends a JSON HTTP POST to the webhook URL within 30 seconds of the event being processed.
2. **Given** a resolved issue that receives a new matching event, **When** the worker marks the issue as regressed, **Then** BugBarn fires the `regression` condition on any matching alert rules.
3. **Given** a rule with `event_count_exceeds` and a threshold of N, **When** an issue accumulates more than N events since the alert was last fired, **Then** BugBarn fires the alert.
4. **Given** an alert that fired, **When** another matching event arrives within the cooldown period (default 15 minutes), **Then** no duplicate POST is sent.
5. **Given** a webhook delivery fails (non-2xx or timeout), **When** the retry limit (3 attempts with exponential backoff) is exhausted, **Then** the failure is logged and no further retries occur for that firing.
6. **Given** a Slack-compatible webhook URL (identified by `hooks.slack.com` host), **When** BugBarn fires the alert, **Then** the payload uses the Slack Block Kit shape so it renders as a formatted message.

**Alert rule schema additions**:

```
webhook_url   TEXT NOT NULL DEFAULT ''   -- delivery target
condition     TEXT NOT NULL DEFAULT 'new_issue'
              -- values: new_issue | regression | event_count_exceeds
threshold     INTEGER NOT NULL DEFAULT 0  -- for event_count_exceeds
cooldown_minutes INTEGER NOT NULL DEFAULT 15
last_fired_at TEXT NOT NULL DEFAULT ''
```

**Generic webhook payload**:

```json
{
  "alert": "My Alert Name",
  "condition": "new_issue",
  "project": "my-app",
  "issue": {
    "id": "42",
    "title": "TypeError: cannot read properties of null",
    "url": "https://bugbarn.example.com/#/issues/42",
    "first_seen": "2026-04-18T10:00:00Z",
    "event_count": 1,
    "severity": "error"
  }
}
```

**Slack Block Kit payload** (when `hooks.slack.com` detected):

```json
{
  "text": "[BugBarn] New issue: TypeError: cannot read properties of null",
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*[BugBarn] New issue in my-app*\n`TypeError: cannot read properties of null`"
      }
    },
    {
      "type": "section",
      "fields": [
        { "type": "mrkdwn", "text": "*Severity*\nerror" },
        { "type": "mrkdwn", "text": "*First seen*\n2026-04-18 10:00 UTC" }
      ]
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "View Issue" },
          "url": "https://bugbarn.example.com/#/issues/42"
        }
      ]
    }
  ]
}
```

**Implementation notes**:
- Delivery runs in the background worker after each successful issue persist, not in the ingest path.
- A `alert_firings` table tracks `(alert_id, fired_at, issue_id)` to enforce cooldowns and prevent duplicates.
- The public base URL for issue deep-links comes from a new `BUGBARN_PUBLIC_URL` env var (defaults to empty, in which case the URL field is omitted from the payload).
- Webhook delivery should time out at 5 seconds and never block worker progress.

---

### User Story P2-2 — Known-Noise Issues Can Be Silenced

As a user, I want to mute an issue indefinitely or until it next regresses, so it stops cluttering the issue list without being lost.

**Why this priority**: Without mute, resolved-but-recurring noise forces you to resolve the same issue repeatedly. The issue list becomes unusable when flaky or expected errors are present.

**Independent Test**: Mute an issue. Confirm it disappears from the default list. Send new events with the same fingerprint. Confirm the issue reappears as `regressed` if the mute mode was `until_regression`, or stays hidden if the mode was `forever`.

**Acceptance Scenarios**:

1. **Given** an issue with any status, **When** the user clicks Mute, **Then** the issue status changes to `muted` and disappears from the default issue list (which filters to `open` + `regressed` by default).
2. **Given** a muted issue with mute mode `until_regression`, **When** a new event arrives with the same fingerprint, **Then** the issue status changes to `regressed` and reappears in the issue list.
3. **Given** a muted issue with mute mode `forever`, **When** new events arrive, **Then** the issue remains `muted` and events are still recorded; the issue is only accessible by filtering to show muted issues.
4. **Given** the issue list UI, **When** the user switches the status filter to `muted`, **Then** muted issues are shown.
5. **Given** a muted issue, **When** the user clicks Unmute, **Then** the issue returns to `unresolved`.

**Status values**: `unresolved` | `resolved` | `muted` | `regressed`

**Schema change**: `status` column already exists. Add `mute_mode TEXT NOT NULL DEFAULT ''` column (`until_regression` | `forever` | empty).

**UI changes**:
- Issue detail: add Mute button with a dropdown for mute mode (alongside the existing Resolve button).
- Issue list status filter: add "Muted" option.
- Default filter: show `unresolved` and `regressed` statuses (currently shows all open).

---

### User Story P2-3 — Issue List Shows Frequency Trend

As a user, I want each issue in the list to show a 24-hour event sparkline, so I can tell at a glance whether something is spiking now, steady, or dying down.

**Why this priority**: Total count and last-seen timestamp don't distinguish a spike from a slow burn. The sparkline makes triage order obvious without opening each issue.

**Independent Test**: Seed events for two issues — one with all events in the last hour, one spread across 24 hours. Open the issue list. Both sparklines must render with bars proportional to their hourly distribution.

**Acceptance Scenarios**:

1. **Given** an issue with events in the last 24 hours, **When** the issue list renders, **Then** each issue row shows a 24-bar mini chart where each bar represents one hour and height is proportional to event count in that hour.
2. **Given** an issue with no events in the last 24 hours, **When** the issue list renders, **Then** the sparkline area is empty or shows a flat baseline — not an error.
3. **Given** the issue list API response, **When** the frontend requests issues, **Then** the response includes a `hourly_counts` array of 24 integers (oldest to newest) alongside existing issue fields.

**API change**: `GET /api/v1/issues` response adds `hourly_counts: [int × 24]` per issue — counts of events in each of the last 24 whole hours (UTC), index 0 = 24 hours ago, index 23 = current partial hour.

**Implementation note**: A single `GROUP BY strftime('%Y-%m-%dT%H', observed_at)` query across all issues in the result set (not N per-issue queries) keeps the list endpoint fast.

**UI implementation**: Pure CSS bar chart — a flex row of 24 `<span>` elements with `height` set to a percentage. No JS charting library. Fits within the existing issue row height.

---

## Phase 3

### User Story P3-1 — Events Carry User Identity

As a developer, I want to attach a user identity to captured events so I can answer "who experienced this bug?" without digging through logs.

**Why this priority**: Anonymous errors require cross-referencing external systems to find affected users. User context collapses that to a single click.

**Independent Test**: Initialize an SDK with `setUser({id: "u-123", email: "user@example.com"})`. Throw an error. Verify the event stored in BugBarn contains the user fields and the event detail UI shows the user identity section.

**Acceptance Scenarios**:

1. **Given** an SDK initialized with `setUser({id, email, username})`, **When** any subsequent exception is captured, **Then** the event payload includes the user context fields.
2. **Given** an event with user context, **When** it is stored and later fetched via the API, **Then** `user.id`, `user.email`, and `user.username` are present in the response (email and username are scrubbed by the PII scrubber, stored only in the `user` structured field which is exempt from flat-key PII scrubbing).
3. **Given** an issue detail page, **When** the event has user context, **Then** the UI shows a User section with id, email, and username.
4. **Given** the facet system, **When** user context is stored, **Then** `user.id` is indexed as a facet key so issues can be filtered by `?user.id=u-123`.
5. **Given** `clearUser()` is called, **When** a subsequent error is captured, **Then** the event has no user context.

**Event model addition**:

```go
type UserContext struct {
    ID       string `json:"id,omitempty"`
    Email    string `json:"email,omitempty"`
    Username string `json:"username,omitempty"`
}
```

Added to `event.Event` as `User UserContext`.

**SDK API**:

```typescript
// TypeScript
BugBarn.setUser({ id: "u-123", email: "alice@example.com", username: "alice" });
BugBarn.clearUser();
```

```python
# Python
bugbarn.set_user(id="u-123", email="alice@example.com", username="alice")
bugbarn.clear_user()
```

```php
// PHP
Client::setUser(id: 'u-123', email: 'alice@example.com', username: 'alice');
Client::clearUser();
```

**PII note**: Email and username are PII. The `user` block is stored as a structured JSON sub-object and intentionally not flattened through the key-based PII scrubber (which operates on string values in arbitrary maps). The `user.email` value is stored as-is; operators who want to omit it should not set it. This is consistent with Sentry's behaviour — the user block is opt-in.

---

### User Story P3-2 — Events Include a Breadcrumb Trail

As a developer, I want to see the sequence of console logs, navigation events, and HTTP requests that happened before a crash, so I can understand what the user was doing rather than just where the crash occurred.

**Why this priority**: Stack traces alone are often insufficient for frontend errors. A breadcrumb trail answers "how did we get here" without requiring a reproduction path.

**Independent Test**: In a TypeScript browser app, trigger console.warn, navigate between routes, make a fetch call, then throw an uncaught error. Verify the captured event contains all three breadcrumbs in chronological order, and the UI renders them as a timeline.

**Acceptance Scenarios**:

1. **Given** the TypeScript SDK is initialized with `autoBreadcrumbs: true` (default), **When** `console.log/warn/error` is called, **Then** a breadcrumb of category `console` is added to the ring buffer.
2. **Given** the TypeScript SDK in a browser context, **When** a `fetch` or `XMLHttpRequest` is made, **Then** a breadcrumb of category `http` is added with `method`, `url`, and `status_code`.
3. **Given** the TypeScript SDK in a browser context, **When** `window.location` changes (hash or pushState), **Then** a breadcrumb of category `navigation` is added with `from` and `to`.
4. **Given** an event is captured, **When** breadcrumbs have been recorded, **Then** the event payload includes a `breadcrumbs` array of up to 100 entries (oldest dropped first — ring buffer), each with `timestamp`, `category`, `message`, and optional `data` map.
5. **Given** an event detail page, **When** the event has breadcrumbs, **Then** the UI renders a Breadcrumbs section as a chronological timeline above the stacktrace, showing category icon, timestamp, and message.
6. **Given** the Python or PHP SDK, **When** `add_breadcrumb` is called manually, **Then** the breadcrumb is included in the next captured event. (Automatic interception is TypeScript-only; Python/PHP support manual-only.)

**Breadcrumb shape**:

```json
{
  "timestamp": "2026-04-18T10:00:00.123Z",
  "category": "navigation",
  "message": "Navigated to /#/issues/42",
  "level": "info",
  "data": { "from": "/#/issues", "to": "/#/issues/42" }
}
```

**Event model addition**:

```go
type Breadcrumb struct {
    Timestamp string         `json:"timestamp"`
    Category  string         `json:"category"`
    Message   string         `json:"message"`
    Level     string         `json:"level,omitempty"`
    Data      map[string]any `json:"data,omitempty"`
}
```

Added to `event.Event` as `Breadcrumbs []Breadcrumb`.

**SDK ring buffer**: 100-entry cap. Each automatic interceptor appends to the shared module-level ring buffer. `captureException` snapshots the buffer at the moment of capture. `clearBreadcrumbs()` resets it.

**TypeScript auto-interceptors**:
- `console`: wrap `console.log/warn/error/info/debug` — category `console`, level mirrors console method.
- `fetch`: wrap `window.fetch` — category `http`, add breadcrumb after response resolves.
- `XMLHttpRequest`: wrap `open`/`send` — category `http`.
- Navigation: listen for `hashchange` and wrap `history.pushState/replaceState` — category `navigation`.

All interceptors are installed lazily on first call to `BugBarn.init()` and are idempotent.

---

## Dogfooding — BugBarn Monitors Itself

### User Story P2-4 — BugBarn Reports Its Own Runtime Errors to BugBarn

As an operator, I want the BugBarn service to report its own panics, dead-lettered records, and persistent worker failures to a BugBarn project, so I am notified through the same alert channel when the error tracker itself has a problem.

**Why this priority**: If BugBarn is silently dropping events or panicking, the only evidence today is in `stderr` logs. Dogfooding gives operational visibility through the same UI and alert pipeline the operator already uses, and validates the Go SDK in a production workload.

**Independent Test**: Trigger a panic recovery in the BugBarn HTTP handler. Verify an issue appears in the BugBarn UI under the `bugbarn-service` project, with the correct stack trace and the event count incrementing on repeat.

**Acceptance Scenarios**:

1. **Given** the service is configured with `BUGBARN_SELF_ENDPOINT` and `BUGBARN_SELF_API_KEY`, **When** the HTTP handler recovers from a panic, **Then** the panic is captured and sent to the configured BugBarn ingest endpoint asynchronously.
2. **Given** a spool record that exceeds the retry limit and is dead-lettered, **When** the worker appends it to `deadletter.ndjson`, **Then** a BugBarn event is captured describing the dead-letter with the ingest ID and last processing error.
3. **Given** the Go SDK is initialized with `BUGBARN_SELF_*` variables, **When** the service shuts down gracefully, **Then** the SDK flushes any queued events within a 2-second bounded timeout.
4. **Given** `BUGBARN_SELF_ENDPOINT` is not set, **When** the service starts, **Then** no SDK is initialized and no self-reporting occurs — the service runs identically to today.
5. **Given** the self-reporting endpoint is the same instance (loopback), **When** the service captures an error in the ingest path, **Then** the capture enqueues to the SDK's in-process channel and is sent after the request response, avoiding a synchronous self-call in the hot path.

**Go SDK** (`sdks/go/`):

The Go SDK mirrors the Python SDK's transport model: a buffered channel drained by a single background goroutine, with `Flush(timeout)` and `Shutdown()`.

```go
// Public API
func Init(opts Options) error
func CaptureError(err error, opts ...CaptureOption) bool
func CaptureMessage(msg string, opts ...CaptureOption) bool
func Flush(timeout time.Duration) bool
func Shutdown(timeout time.Duration) bool

// Panic recovery helper (wraps http.Handler)
func RecoverMiddleware(next http.Handler) http.Handler
```

```go
type Options struct {
    APIKey      string
    Endpoint    string
    ProjectSlug string
    Release     string
    Environment string
    QueueSize   int           // default 256
}

type CaptureOption func(*captureOpts)

func WithAttributes(attrs map[string]any) CaptureOption
func WithUser(id, email, username string) CaptureOption
```

**Integration points in `cmd/bugbarn/main.go`**:
- `init()` — call `bugbarn.Init` when `BUGBARN_SELF_ENDPOINT` is set.
- HTTP server middleware — wrap `ServeHTTP` with `bugbarn.RecoverMiddleware`.
- Worker dead-letter path — call `bugbarn.CaptureMessage` with the dead-letter details.
- Graceful shutdown — call `bugbarn.Shutdown(2 * time.Second)` before process exit.

**Self-reporting project setup** (documented in `docs/operations.md`):

```sh
# Create a project for BugBarn itself
bugbarn project create --name="BugBarn Service"

# Create an ingest-only key (so the self-reporting key can't access the API)
bugbarn apikey create --project=bugbarn-service --name=self-report --scope=ingest
# → bb_live_<key>

# Set env vars (can point at itself — localhost is fine)
BUGBARN_SELF_ENDPOINT=http://localhost:8080/api/v1/events
BUGBARN_SELF_API_KEY=bb_live_<key>
BUGBARN_SELF_PROJECT=bugbarn-service
```

**Circular dependency note**: Self-reporting uses the same ingest endpoint as any other client, but the SDK sends asynchronously after the HTTP response is already sent. The ingest path itself does not call the SDK — only the panic recovery middleware and worker dead-letter code do. There is no risk of infinite self-reporting loops.

---

## Requirements

### Functional Requirements

- **FR-101**: Alert rules MUST support `webhook_url`, `condition`, `threshold`, `cooldown_minutes`, and `last_fired_at` fields.
- **FR-102**: Alert delivery MUST support generic JSON webhooks and Slack Block Kit payloads (auto-detected by host).
- **FR-103**: Alert delivery MUST enforce a per-rule cooldown and MUST NOT fire duplicate notifications within the cooldown window.
- **FR-104**: Alert delivery MUST be attempted with retries (3 attempts, exponential backoff) and MUST NOT block worker progress on failure.
- **FR-105**: Issues MUST support `muted` and `regressed` statuses in addition to `unresolved` and `resolved`.
- **FR-106**: Muted issues MUST NOT appear in the default issue list filter.
- **FR-107**: Muted issues with mode `until_regression` MUST transition to `regressed` when a new event arrives.
- **FR-108**: The issue list API MUST return `hourly_counts` (24 integers) per issue without a per-issue subquery.
- **FR-109**: The web UI MUST render a 24-bar sparkline per issue row using only CSS (no JS charting library).
- **FR-110**: The canonical event model MUST support a `user` context object with `id`, `email`, and `username`.
- **FR-111**: `user.id` MUST be indexed as a facet key for issue filtering.
- **FR-112**: TypeScript, Python, and PHP SDKs MUST expose `setUser` / `clearUser`.
- **FR-113**: The canonical event model MUST support a `breadcrumbs` array capped at 100 entries per event.
- **FR-114**: The TypeScript SDK MUST automatically intercept `console`, `fetch`/`XHR`, and navigation events when `autoBreadcrumbs: true` (default).
- **FR-115**: Python and PHP SDKs MUST support manual `add_breadcrumb` calls.
- **FR-116**: The event detail UI MUST render breadcrumbs as a chronological timeline section when present.
- **FR-117**: A Go SDK MUST exist at `sdks/go/` with `Init`, `CaptureError`, `CaptureMessage`, `Flush`, `Shutdown`, and `RecoverMiddleware`.
- **FR-118**: When `BUGBARN_SELF_ENDPOINT` is set, the service MUST use the Go SDK to report panics and dead-lettered records to the configured endpoint.
- **FR-119**: Self-reporting MUST be entirely opt-in; absence of `BUGBARN_SELF_ENDPOINT` MUST leave behaviour identical to today.

### Non-Functional Requirements

- **NFR-101**: Alert webhook delivery MUST complete or time out within 5 seconds and MUST NOT add more than 100 ms to worker batch processing time in the common case (delivery is fire-and-forget in a separate goroutine).
- **NFR-102**: The `hourly_counts` query MUST not degrade issue list response time by more than 50 ms at 10,000 issues.
- **NFR-103**: The breadcrumb ring buffer in the TypeScript SDK MUST use O(1) insertion (circular buffer or fixed-size array with head pointer).
- **NFR-104**: The Go SDK transport goroutine MUST not prevent process exit; it MUST respect a bounded `Shutdown` timeout.

---

## Success Criteria

- **SC-101**: An alert rule with a webhook URL fires within 30 seconds of a matching new issue, with the correct JSON payload.
- **SC-102**: A muted issue does not appear in the default issue list and reappears as `regressed` when `until_regression` mode is active.
- **SC-103**: The issue list sparklines correctly show a spike in the most recent hours for a freshly seeded issue.
- **SC-104**: A TypeScript event captured after `setUser` contains `user.id` and `user.email` in the stored event.
- **SC-105**: A TypeScript browser event captured after three console calls, a fetch, and a navigation contains five breadcrumbs in chronological order.
- **SC-106**: A BugBarn panic recovery produces an issue in the BugBarn UI under the self-reporting project.

---

## Key Entities (additions)

- **Alert Firing**: Record of a specific alert rule firing for a specific issue, used to enforce cooldown windows.
- **User Context**: Optional structured identity block on an event — id, email, username.
- **Breadcrumb**: Timestamped structured log entry captured before an event, stored as a JSON array on the event row.
- **Go SDK**: Native Go error reporting client used by the BugBarn service to self-report.

---

## Open Questions

1. Should Discord webhooks get a special payload shape (Discord uses `content` + `embeds` rather than Slack Block Kit)? Recommendation: auto-detect by `discord.com/api/webhooks` host and emit a Discord embed payload.
2. Should `user.email` be passed through the PII scrubber or treated as intentional? Recommendation: intentional — callers who don't want email stored should not set it. Document clearly.
3. Should breadcrumbs be stored in the `events` table as a JSON column or in a separate `event_breadcrumbs` table? Recommendation: JSON column on `events` — breadcrumbs are always read with the event and never queried independently.
4. Should the Go SDK live in `sdks/go/` (separate Composer-style package) or in `internal/reporter/` (internal-only)? Recommendation: `sdks/go/` — a real importable module validates that it works as an external dependency, and it can be used by any Go app beyond BugBarn itself.
