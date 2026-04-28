# BugBarn Overview

BugBarn is a lightweight, self-hosted error tracking system built in Go with SQLite. It is designed for individual developers, small teams, and self-hosters who want error visibility without the cost or privacy tradeoffs of a SaaS provider such as Sentry.

A single binary with no external runtime dependencies. Drop it on a server, point your SDKs at it, and errors start appearing.

---

## Who it is for

- Individual developers running personal projects on a VPS or home server
- Small teams that want full control over where error data lives
- Anyone who finds hosted error tracking too expensive or too invasive for their use case

---

## The problem it solves

When something breaks in production you need to know: what happened, where in the code, how often, and whether it is new or recurring. Hosted error trackers solve this well but at a cost — monthly fees that scale with event volume, and your stack traces and user data leaving your infrastructure.

BugBarn stores everything locally in a SQLite file. Events are accepted asynchronously, grouped into issues by fingerprint, and surfaced through a TypeScript SPA. No external services required, no data leaves your server.

---

## Key capabilities

- **Issue grouping** — errors are grouped by a SHA256 fingerprint (a unique signature derived from the error type, message, and stack trace — identical errors group into one issue) derived from exception type, message, stack trace, and stable context keys (keys that reliably identify where the error came from, like service name, environment, and HTTP route). Duplicates collapse into a single issue rather than flooding your feed.
- **Issue statuses** — unresolved, resolved, regressed, muted (until next regression or forever). Resolved issues automatically reopen when a new event arrives.
- **Alerts** — rule-based notifications on `new_issue`, `regression`, and `event_count_exceeds` (with a configurable threshold). Delivers to Slack (Block Kit), Discord (Embeds), or any generic webhook. Retries three times with backoff; configurable cooldowns.
- **Weekly digest** — one email per week summarising all projects with activity: total events, new issues, resolved issues, regressions, and the top five issues per project. Also fires a JSON webhook.
- **Log ingestion and streaming** — structured log lines accepted via POST and streamed to the UI in real time via Server-Sent Events. Filterable by level and search string.
- **Releases** — track deployments against issues.
- **Multi-project** — all data is project-scoped. A single BugBarn instance serves any number of projects. An all-projects view aggregates across them.
- **Source map symbolication** — JavaScript stack traces are symbolicated server-side using uploaded source maps.
- **Privacy scrubbing** — automatic redaction of sensitive keys (`password`, `token`, `secret`, `email`, `cookie`, `authorization`, `api_key`, `session`, `csrf`) and pattern-matched values (email addresses, IPs, UUIDs, bearer tokens) before storage.
- **Self-reporting** — BugBarn can report its own errors to itself (dogfooding).
- **SDKs** — official clients for Go (`bugbarn-go`), TypeScript, and Python. All support `Init`, `CaptureError`, `CaptureMessage`, and `Shutdown`.
- **Authentication** — session cookies (HMAC-signed, bcrypt passwords) or API keys (SHA256-hashed, scoped to `full` or `ingest`, optionally per-project).

---

## Performance

BugBarn is designed to stay out of the way of your application. The ingest path is non-blocking by design: when your SDK sends an error event, the HTTP handler writes it to an in-memory queue and returns 202 Accepted immediately — it never touches the database during that request. A background flush writes batches to a durable spool file on disk every 5 milliseconds or every 64 records, whichever comes first. Your application's error-reporting call completes in microseconds regardless of what the database is doing.

Go's goroutine model means the HTTP server handles thousands of concurrent connections with negligible overhead. Each connection gets its own lightweight goroutine; the runtime multiplexes these across available CPU cores automatically.

**Load test results** — tested against the production deployment (k8s, `500m` CPU limit, `256Mi` memory limit) over the internet using `hey`:

| Concurrency | Throughput | Avg latency | p99 latency | CPU | Memory |
|---|---|---|---|---|---|
| Idle | — | — | — | 58m | 34Mi |
| 25 | ~585 req/s | 43ms | 107ms | 55m | 35Mi |
| 200 | ~2,050 req/s | 97ms | 198ms | 444m | 54Mi |
| 500 | ~2,830 req/s | 175ms | 502ms | 494m | 168Mi |

All responses were `202` — zero errors across all tests. **CPU was the bottleneck** in every run, not memory or disk. At 500 concurrent connections the pod was at 99% of its CPU limit (`494m/500m`); raising the limit would proportionally increase throughput. Memory only spiked materially at extreme concurrency (500 goroutines × stack allocations + buffered request bodies).

The spool acts as a shock absorber. The 79,000 events ingested during the test were accepted immediately; the background worker then drained the spool at maximum CPU rate over the following minutes — none of that backlog processing affected ingest availability. This is the spool's purpose: decouple ingest throughput from storage throughput.

When limits are reached, BugBarn degrades gracefully. If the spool grows beyond the configured maximum size (`BUGBARN_MAX_SPOOL_BYTES`), ingest returns `429 Too Many Requests` with a `Retry-After` header. Log entries are trimmed to the most recent 10,000 per project. Facet keys and values beyond the cardinality limits are silently dropped.

See [Performance and limits](deployment/performance.md) for hardware recommendations and detailed guidance.

---

## What BugBarn is NOT

- **Not an APM tool.** BugBarn tracks errors and logs. It does not instrument performance, traces, or metrics.
- **Not horizontally scalable.** SQLite means a single writer. The deployment strategy is `Recreate` (not `RollingUpdate`). You run one replica. If you need multi-region active-active error ingestion, BugBarn is the wrong tool.
- **Not a Sentry replacement at scale.** It handles the use cases that matter for small deployments. High-volume, enterprise-scale ingestion is out of scope.

---

## Architecture

Events flow through a durable local spool so the ingest endpoint can return immediately and never block the caller waiting for a database write.

```mermaid
flowchart LR
    SDK["SDK\n(Go / TS / Python)"]
    Ingest["POST /api/v1/events\nPOST /api/v1/logs"]
    Spool["Local spool\n(disk queue)"]
    Worker["Background worker"]
    DB["SQLite\n(WAL mode)"]
    API["REST API"]
    UI["TypeScript SPA"]

    SDK --> Ingest
    Ingest -- "202 Accepted" --> SDK
    Ingest --> Spool
    Spool --> Worker
    Worker --> DB
    DB --> API
    API --> UI
```

**Litestream** can optionally replicate the SQLite WAL to object storage for disaster recovery.

---

## Further reading

- [Getting started](getting-started.md) — run BugBarn locally and send your first error in under five minutes
- [Architecture](architecture.md) — database schema, spool format, background workers, and SSE
- [Operations](operations.md) — production deployment, Kubernetes manifests, Litestream, backup and restore
