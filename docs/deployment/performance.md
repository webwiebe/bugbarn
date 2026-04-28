# Performance and Limits

Practical reference for operators. Covers ingest throughput, processing throughput, read performance, spool sizing, hardware recommendations, system limits, and self-monitoring.

---

## Ingest throughput

The ingest endpoint (`POST /api/v1/events`, `POST /api/v1/logs`) is non-blocking. The HTTP handler writes each request payload to an in-memory queue (32,768 entry capacity) and returns 202 Accepted immediately. A background flush goroutine drains that queue to the spool file every 5 ms or every 64 records, whichever comes first. No database write happens in the request path.

The practical bottleneck chain for ingest is:

```
Network bandwidth  >  app (JSON parsing + body size check)  >  spool disk write
```

On a gigabit LAN or fast internet connection, the application becomes the bottleneck before the network does only if events are very large. On a slow disk (SD card, spinning HDD), spool writes may become the ceiling, but this is unusual in practice because the flush cadence is low and batched.

**What this means for sizing:** ingest capacity is largely a function of network and disk I/O, not CPU. A single-core machine handles a high event rate without issue as long as the spool disk is reasonably fast.

---

## Processing throughput

A single background worker goroutine reads from the spool and writes events to SQLite. It runs approximately once per second. Single-threaded processing is intentional — it avoids SQLite write contention and keeps the system predictable.

Factors that affect processing rate:

| Factor | Impact |
|---|---|
| Disk speed (NVMe vs SD card) | Largest variable; NVMe can handle thousands of simple inserts per second in WAL mode, SD cards are significantly slower |
| Source map lookups | JavaScript events with source maps require file I/O for symbolication; this adds latency per event if maps are large |
| Fingerprint computation | SHA256 over exception type, message, and stack trace; negligible cost in practice |
| Event payload size | Larger payloads mean more bytes to parse and store |
| SQLite page cache | More available RAM means more of the database fits in memory, reducing disk reads during inserts that touch indexes |

The processing worker is not a throughput bottleneck under normal conditions. It is designed to drain the spool reliably over time. If you consistently produce events faster than the worker can process them, the spool grows. Size `BUGBARN_MAX_SPOOL_BYTES` accordingly (see below).

---

## Read performance

BugBarn uses SQLite in WAL (Write-Ahead Logging) mode. WAL allows multiple concurrent readers to run without blocking each other and without blocking the writer. All read API endpoints — list issues, get event detail, search logs — run concurrently.

Key factors for read performance:

- **SQLite page cache**: SQLite uses available memory to cache database pages. More RAM means more of your indexes and hot rows stay in cache, which reduces disk reads on repeated queries. The default `PRAGMA cache_size` is inherited from the SQLite default; you can tune this via `BUGBARN_SQLITE_CACHE_KB` if the variable is exposed.
- **Index coverage**: Issues are indexed on project, status, fingerprint, and timestamp. Event lookups by issue are indexed. Log queries are indexed by project and timestamp. Full-text search across event payloads is a sequential scan and is slower on large datasets.
- **Database size**: SQLite reads scale well into the hundreds of megabytes on any modern disk. If your database grows beyond a few gigabytes, query latency for unindexed scans will increase noticeably.

---

## Spool sizing

The spool is a durable NDJSON file on disk that buffers events between ingest and the background worker. It absorbs traffic spikes and survives application restarts — unprocessed events are replayed on startup.

**`BUGBARN_MAX_SPOOL_BYTES`** sets the maximum spool file size. When this limit is reached, the ingest endpoint returns 429 Too Many Requests with a `Retry-After` header. This is a backpressure mechanism to prevent disk exhaustion.

How to size it:

1. Estimate your peak event burst size and how long it might last.
2. Estimate average event payload size (usually 1–10 KB depending on stack trace depth and attached context).
3. Set `BUGBARN_MAX_SPOOL_BYTES` high enough to absorb a reasonable spike but low enough to protect available disk space.

Example: if you expect bursts of up to 10,000 events at 5 KB average, that is ~50 MB of spool. A value of `104857600` (100 MB) gives comfortable headroom.

**What 429 means in practice:** the sending SDK will retry with backoff. Events are not lost unless the SDK's retry budget is exhausted. A sustained 429 indicates that the background worker cannot drain the spool as fast as events arrive — investigate disk speed, processing complexity, or reduce event volume.

---

## Hardware recommendations

BugBarn has no minimum hardware requirement. The following tiers reflect typical operational experience.

| Tier | Hardware | Suitable for |
|---|---|---|
| Minimal | Raspberry Pi 4, single-core VPS (512 MB RAM, SD card or slow disk) | Personal projects, low-volume applications, evaluation. Ingest is fine; processing and reads are slower due to storage. |
| Standard | 1–2 vCPU, 1–2 GB RAM, SSD | Small teams, moderate event volume, comfortable headroom. This is the recommended baseline for production. |
| High-volume | 2–4 vCPU, 4 GB RAM, NVMe | Applications with high event rates, large databases, or many concurrent dashboard users. SQLite page cache benefits significantly from the extra RAM. |

BugBarn does not support horizontal scaling — it runs as a single binary with a single SQLite file and a single writer. Vertical scaling (more RAM, faster disk) is the path to higher throughput. If you need multi-region active-active ingestion, BugBarn is the wrong tool.

For disaster recovery, use **Litestream** to replicate the SQLite WAL to object storage. This provides a continuous backup and allows point-in-time restore. See [kubernetes.md](kubernetes.md) for a Litestream configuration example.

---

## Limits

| Limit | Value | Behaviour when exceeded |
|---|---|---|
| Spool size | `BUGBARN_MAX_SPOOL_BYTES` (operator-configured) | 429 Too Many Requests with Retry-After |
| Facet keys per project | 50 distinct keys | New keys beyond the limit are silently dropped |
| Facet values per key | 10,000 distinct values | New values beyond the limit are silently dropped |
| Log entries per project | 10,000 | Oldest entries are trimmed on each insert |
| Concurrent writers | 1 (background worker) | By design; additional writers would cause SQLite contention |
| Horizontal replicas | 1 | Single binary, single SQLite file; use Litestream for read replicas |

---

## Monitoring BugBarn itself

BugBarn can report its own errors to a BugBarn project — or to any compatible error tracking endpoint — using the `BUGBARN_SELF_ENDPOINT` and `BUGBARN_SELF_API_KEY` environment variables. This is useful for catching panics, background worker failures, and database errors in production.

To enable self-reporting, create a dedicated project in your BugBarn instance (or a separate one), generate an ingest-scoped API key, and set:

```
BUGBARN_SELF_ENDPOINT=https://your-bugbarn-instance/api/v1/events
BUGBARN_SELF_API_KEY=<ingest-scoped key>
```

This is optional but recommended for production deployments where you want visibility into BugBarn's own health.
