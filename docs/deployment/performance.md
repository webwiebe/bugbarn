# Performance and Limits

Practical reference for operators. All throughput numbers are from a real load test against the production deployment — not estimates.

---

## Load test methodology

Tests were run with [`hey`](https://github.com/rakyll/hey) against the live production instance at `bugbarn.wiebe.xyz`, over the internet, using a realistic 428-byte event payload (JSON with exception, stack trace, attributes, and resource fields). The test target was `POST /api/v1/events`.

**Deployment under test:**

| | |
|---|---|
| Runtime | Go binary on Kubernetes (k3s) |
| CPU limit | `500m` (0.5 vCPU) |
| Memory limit | `256Mi` |
| CPU request | `100m` |
| Memory request | `128Mi` |
| Storage | PVC on k3s node |

Pod resources were sampled with `kubectl top` immediately after each run.

---

## Results

| Concurrency | Throughput | Avg latency | p50 | p95 | p99 | CPU (post-run) | Memory (post-run) | Errors |
|---|---|---|---|---|---|---|---|---|
| Idle | — | — | — | — | — | 58m | 34Mi | — |
| 25 | 585 req/s | 43ms | 27ms | 95ms | 107ms | 55m | 35Mi | 0 |
| 200 | 2,048 req/s | 97ms | 98ms | 135ms | 198ms | 444m | 54Mi | 0 |
| 500 | 2,828 req/s | 175ms | 170ms | 293ms | 502ms | 494m | 168Mi | 0 |

All responses were `202 Accepted`. Zero `4xx` or `5xx` errors across all runs.

---

## What the numbers mean

**CPU is the bottleneck.** At 200 concurrent connections and ~2,000 req/s, the pod was at 88% of its CPU limit (`444m/500m`). At 500 concurrent connections and ~2,800 req/s, it was at 99% (`494m/500m`). Memory stayed low at moderate concurrency (54Mi at 2,000 req/s); it spiked to 168Mi at 500 concurrent connections because each goroutine carries a stack and the in-flight request bodies are buffered in memory simultaneously.

**Raising the CPU limit directly raises throughput.** Because ingest is CPU-bound (JSON parsing, HMAC key validation, queue writes), doubling the CPU limit from `500m` to `1000m` would approximately double sustainable throughput. Memory overhead is modest — `256Mi` is sufficient headroom for normal workloads; `512Mi` gives more buffer at extreme concurrency.

**The spool absorbs spikes.** The 79,000 events ingested across the three test runs were all accepted immediately. After the test, the spool contained ~60MB of unprocessed events. The background worker then drained the backlog over several minutes, with CPU pinned at ~498m throughout — ingest availability was never affected. This is the architectural intention: ingest and processing are deliberately decoupled.

**Latency is network-dominated at low load.** The 585 req/s run used only 55m CPU (essentially idle); the 43ms average latency at that rate reflects internet round-trip time, not server processing time. On a LAN or within the same datacenter, latency would be significantly lower.

---

## Ingest throughput

The ingest endpoint (`POST /api/v1/events`, `POST /api/v1/logs`) writes each accepted request to an in-memory queue (32,768 entry capacity) and returns `202` immediately. A flush goroutine drains the queue to the spool file every 5ms or every 64 records. No database write occurs in the request path.

The practical bottleneck chain:

```
Network  →  Go HTTP + JSON parsing  →  in-memory queue  →  spool file write
```

Under the tested configuration (500m CPU, gigabit-adjacent internet), the ceiling was ~2,800 req/s before CPU throttling. On a same-datacenter connection and with a relaxed CPU limit, throughput would be substantially higher.

---

## Processing throughput

A single background worker goroutine reads from the spool and writes events to SQLite. The single-threaded design is intentional — it avoids SQLite write contention and keeps processing predictable. During the post-test drain, the worker sustained maximum CPU (`~498m`) continuously, processing tens of thousands of events per minute.

Factors that affect processing rate:

| Factor | Impact |
|---|---|
| Disk speed | Largest variable — NVMe handles thousands of inserts/second in WAL mode; SD card or spinning disk is significantly slower |
| Source map lookups | JavaScript events with uploaded source maps require a blob read per stack frame |
| Event payload size | Larger payloads mean more bytes to parse and more data to store |
| SQLite page cache | More available RAM keeps hot indexes in memory, reducing disk reads during inserts |

---

## Read performance

SQLite WAL mode allows concurrent readers to run independently of each other and of the single writer. All read API endpoints (list issues, get event, search logs, query facets) execute concurrently without blocking ingest.

Read latency under normal conditions:
- Index-covered queries (issue list by status/project, event list by issue): sub-millisecond on a warm page cache
- Full-text search across event payloads: sequential scan, slower on large datasets
- SQLite page cache is bounded by available RAM; more RAM = more hot rows cached = faster repeated queries

---

## Spool sizing

The spool is an append-only NDJSON file. Its size grows as events are ingested and shrinks only when the file rotates (at ~64 MiB). The cursor tracks the last-processed position — file size alone does not indicate backlog depth.

Set `BUGBARN_MAX_SPOOL_BYTES` to protect against disk exhaustion under sustained overload. When the limit is reached, ingest returns `429 Too Many Requests` with a `Retry-After` header.

**Sizing guide:**

```
max_spool = peak_burst_events × avg_event_bytes × safety_factor

Example: 10,000 events × 2 KB average × 3× safety = ~60 MB
→ set BUGBARN_MAX_SPOOL_BYTES=67108864  (64 MiB)
```

Average event size in these tests was ~428 bytes for a minimal payload. Real payloads with deep stack traces, breadcrumbs, and attached context are typically 2–10 KB.

A sustained `429` means the worker cannot drain the spool as fast as events arrive. Check disk speed, CPU availability, and whether source map lookups are adding per-event latency.

---

## Hardware recommendations

| Tier | Spec | Expected throughput | Suitable for |
|---|---|---|---|
| Minimal | 0.5–1 vCPU, 512Mi–1Gi RAM, any disk | ~1,000–3,000 req/s ingest (CPU-bound) | Personal projects, low-volume apps |
| Standard | 1–2 vCPU, 1–2 Gi RAM, SSD | ~5,000–10,000 req/s ingest | Small teams, production workloads |
| High-volume | 2–4 vCPU, 4 Gi RAM, NVMe | ~20,000+ req/s ingest | High event rates, large databases, many concurrent dashboard users |

> Throughput estimates assume same-datacenter or LAN ingestion. The load test was conducted over the internet with a 500m CPU limit and achieved ~2,800 req/s — real LAN throughput at the same CPU allocation would be higher.

**BugBarn does not support horizontal scaling.** Single binary, single SQLite file, single writer. The deployment strategy must be `Recreate` (not `RollingUpdate`). Vertical scaling — more CPU, faster disk, more RAM — is the correct path to higher throughput.

For disaster recovery and read replicas, use [Litestream](kubernetes.md#litestream).

---

## Limits

| Limit | Value | Behaviour when exceeded |
|---|---|---|
| Spool size | `BUGBARN_MAX_SPOOL_BYTES` (operator-set, default unlimited) | `429 Too Many Requests` with `Retry-After` header |
| In-memory ingest queue | 32,768 records | Backpressure to spool flush; effectively never the bottleneck |
| Max request body | `BUGBARN_MAX_BODY_BYTES` (default 1 MiB) | `413 Request Entity Too Large` |
| Facet keys per project | 50 distinct keys | New keys silently dropped |
| Facet values per key | 10,000 distinct values | New values silently dropped |
| Log entries per project | 10,000 | Oldest entries trimmed on each insert |
| Concurrent writers | 1 (background worker) | By design |
| Horizontal replicas | 1 | Single binary, single SQLite file |

---

## Monitoring BugBarn itself

BugBarn can report its own panics and background worker failures to a BugBarn project using the self-reporting env vars:

```
BUGBARN_SELF_ENDPOINT=https://your-bugbarn-instance
BUGBARN_SELF_API_KEY=<ingest-scoped key>
```

The production instance at `bugbarn.wiebe.xyz` has self-reporting enabled and reports to itself.
