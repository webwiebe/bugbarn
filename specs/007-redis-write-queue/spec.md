# Feature Specification: Redis Write Queue (CQRS command bus)

**Feature Branch**: `007-redis-write-queue`
**Created**: 2026-06-11
**Status**: Implemented (phases 1-4); cutover (phase 5) pending production validation
**Input**: Production writer wedged during a maintenance window (2026-06-11). Root cause was the reader→writer **HTTP write-forwarding + per-pod disk spool** coupling introduced in [006](../006-cqrs-read-write-split/spec.md).

## Problem

The 006 split made readers forward writes to the single writer over **internal HTTP**, backed by a **per-pod disk spool**. That coupling is fragile under load:

1. Writer gets slow → reader forwards **time out** → readers **retry** → HTTP connections pile up on the writer → process exhausts **inotify instances / FDs** ("too many open files") → writer **wedges** → spools can't drain → death spiral, amplified by selflog (write-timeout → ERROR log → self-report → more writes).
2. The writer also performs **synchronous DB writes inside HTTP request handlers** (log ingest, release markers) that compete with the background event worker for the single SQLite write connection.
3. Backpressure is expressed as **HTTP timeouts**, the worst possible signal — it triggers retries that make the overload worse.

On 2026-06-11 this produced a ~1.3 GB combined spool backlog across writer + readers that would not drain, and required several disruptive restarts.

This is exactly the failure mode **spanbarn** avoids by decoupling writes through a Redis queue.

## Solution

Introduce a **durable Redis list** as the write queue (CQRS command bus). High-volume ingest (events + logs) flows:

```
reader (producer)  →  local spool (durability anchor)  →  LPUSH  →  Redis list  →  BRPOP  →  writer (single consumer)  →  SQLite
```

- Readers `LPUSH` write batches and return immediately; they never block on the writer.
- The single writer `BRPOP`s and drains at its own pace. No HTTP storm, no backpressure on any request path.
- The writer serialises **all** DB writes (event persist, log insert, retention, WAL checkpoint) behind a **shared write mutex**, so nothing competes for the SQLite write lock — and the periodic `wal_checkpoint(TRUNCATE)` finally gets clean windows (the [project_wal_checkpoint] finding: that checkpoint pattern only works under serialisation).
- The reader-side **local spool remains the durability anchor**: its cursor advances only after a successful `LPUSH`, so a Redis outage backs the spool up without data loss (identical durability to today, only the forwarding target changes from HTTP→writer to spool→Redis).

### Scope: what moves to the queue

| Path | Volume | Today | After |
|---|---|---|---|
| Event ingest (`/api/v1/events`, OTLP) | high | reader spool → HTTP → writer spool → worker | reader spool → **Redis** → writer consumer |
| Log ingest (`/api/v1/logs`) | high | reader → HTTP proxy → writer `logs.Insert` (sync) | reader spool → **Redis** → writer consumer |
| Dashboard mutations (resolve/mute, settings, releases, alerts, api keys, source maps) | low | reader → HTTP proxy → writer (sync, needs response) | **unchanged** — stay on `WriteForwarder` (sync HTTP) |

Low-volume dashboard mutations need a synchronous response (e.g. resolve issue → return the updated issue), so they stay on the existing HTTP proxy. They were never the storm source.

## Architecture

```
        OTLP / SDK ingest                         dashboard reads & mutations
               │                                          │
        ┌──────▼────────────── reader pods (N, HPA) ──────▼─────────────┐  QUERY side
        │ ingest handler → local spool (durability anchor)              │  (read-only SQLite,
        │   └─ RedisForwarder: LPUSH batches ──────────┐                │   WAL concurrent reads)
        │ dashboard mutations ── WriteForwarder (sync HTTP) ──┐         │
        └────────────────────────────────────────────────────┼─────────┘
                                                       │      │
                              Redis list  bugbarn:write-queue │ (sync writes, low volume)
                                                       │      │
        ┌──────────────────────────────────────────────▼──────▼────────┐  COMMAND side
        │ writer pod (1):  RedisConsumer BRPOP                          │
        │   shared writeMu serialises ALL writes:                      │
        │     event persist · log insert · retention · WAL checkpoint  │
        │            → single SQLite writer connection                 │
        │   + HTTP server for sync dashboard mutations (also under mu) │
        └───────────────────────────────────────────────────────────────┘
                                   │
                              Litestream → S3
```

## Design details

### Queue (`internal/queue`)
Adapted from spanbarn's `internal/queue/redis_queue.go`:
- `WriteQueueKey = "bugbarn:write-queue"`, batches of ≤ N records per list item.
- `Publish(ctx, []QueueItem)` → `LPUSH`. `Consume(ctx)` → `BRPOP` with timeout (returns `nil,nil` on timeout so callers loop). `Len(ctx)` for depth metrics.
- `NewRedisQueueWithRetry` for writer-mode startup (queue pod may come up after the writer during a rolling deploy).

### QueueItem envelope
A queue item must carry enough to reconstruct the write on the consumer:
```go
type QueueItem struct {
    Kind        string // "event" | "log"
    ProjectSlug string
    ContentType string
    ReceivedAt  time.Time
    BodyBase64  string // raw request body (same as today's spool.Record)
}
```
This reuses the existing `spool.Record` shape so the reader spool and the queue carry the same payload.

### Producer (reader side)
- New `RedisForwarder` (modeled on `SpoolForwarder.Drain`): reads the local ingest spool, `LPUSH`es batches, advances the cursor only on success.
- Log ingest on readers is appended to the **same spool** (as a `Kind:"log"` record) instead of HTTP-proxied, so logs get the same durable, decoupled path as events.

### Consumer (writer side)
- New `RedisConsumer` (modeled on spanbarn's `RedisWorker.Run`): `BRPOP` loop → dispatch by `Kind` (event → `PersistProcessedEvent` pipeline; log → `logs.Insert`).
- Holds the **shared write mutex** for the DB-write phase of each batch.

### Shared write mutex
A single `*sync.Mutex` (or a small `writelock` type) passed to: the Redis consumer, the retention/digest/analytics writers, and the WAL checkpointer. Every code path that opens a write transaction acquires it first. This makes the single SQLite writer connection genuinely single-writer at the application level and lets the TRUNCATE checkpoint run without `SQLITE_BUSY` churn.

### Durability & ordering
- **At-least-once**: cursor advances only after `LPUSH` success (producer) and the consumer acks by `BRPOP` (Redis removes on pop) only after a successful insert — on consumer crash mid-batch, the batch is lost from Redis but **still in the reader spool** until the reader's cursor... ⚠️ **OPEN QUESTION**: BRPOP removes the item before the insert completes. To get at-least-once on the consumer we need either (a) `BRPOPLPUSH` into a processing list + remove-on-success, or (b) accept at-most-once for the Redis hop and rely on the reader spool retas the anchor. Spanbarn accepts the simple BRPOP model; document the trade-off and decide. Likely (a) `BLMOVE` to a per-consumer processing list.
- **Ordering**: events are fingerprint-grouped and idempotent-ish; strict ordering not required. Logs are append-only. Per-project ordering is preserved well enough by FIFO list semantics.

### Backpressure
- Producers never block on the writer. If Redis fills (memory), `LPUSH` fails → reader spool accumulates (bounded by `MaxSpoolBytes`) → oldest spool rotated/dropped, same as today.
- `queue.Len()` exported as a metric / health signal instead of HTTP timeouts.

## Config & deployment

- New config: `BUGBARN_REDIS_QUEUE_URL` (e.g. `redis://bugbarn-redis-queue:6379/0`). **Optional** — when empty, fall back to today's HTTP-forward + spool path (feature flag for safe rollout).
- New dependency: `github.com/redis/go-redis/v9`.
- New k8s: `deployment-redis-queue.yaml` + `service-redis-queue.yaml` (1 replica, `appendonly yes` for AOF durability), `BUGBARN_REDIS_QUEUE_URL` in the SOPS secret for reader+writer.
- Raise node `fs.inotify.max_user_instances` (independent hardening; mitigates the leak class).

## Phased implementation plan

- **Phase 1 — Foundation (no behavior change).** ✅ `internal/queue` package + `Item` envelope + `BUGBARN_REDIS_QUEUE_URL` config + go-redis dependency. Unit tests with miniredis.
- **Phase 2 — Writer consumer.** ✅ `internal/ingestproc` — `Processor.PersistRecord` (extracted event pipeline) + `Consumer` (BRPOP → dispatch). Wired into the writer's `run()` behind the flag; connects in a goroutine so startup never blocks on Redis. The shared write mutex is plumbed (nil for now) and activates when retention + the WAL checkpoint move under it (deferred — see note below).
- **Phase 3 — Reader producer.** ✅ `SpoolForwarder` Redis drain (`NewRedisSpoolForwarder`); `internal/logparse` shared pino parsing; consumer `KindLog` path. Reader uses the Redis spool when configured.
- **Phase 4 — Deploy.** ✅ `redis-queue-deployment.yaml`/`-service.yaml` for staging + production; `BUGBARN_REDIS_QUEUE_URL` on writer + reader; kustomizations updated. Rollout is staged by the pipeline (staging on tag, prod manual).
- **Phase 5 — Cutover & cleanup.** ⏳ *Deferred until the Redis path is validated in production.* Then retire the reader→writer ingest HTTP path (`SpoolForwarder` HTTP send) and the writer's inbound ingest HTTP handler, and fold the file-spool worker's inline pipeline into `ingestproc`. Keep `WriteForwarder` for sync dashboard mutations. The feature flag stays as the rollback path until this happens.

### Decisions made during implementation

- **Consumer delivery:** plain `BRPOP` (matches spanbarn) — at-most-once on the consumer hop; a writer crash mid-batch loses that batch. Acceptable for telemetry; the reader spool covers un-published data. A `BLMOVE`-to-processing-list upgrade is the path to exactly-once if needed.
- **Write mutex deferred:** `MaxOpenConns(1)` already serialises writes at the pool level, so the consumer passes `nil` for now. The shared mutex becomes meaningful when the app-side WAL `TRUNCATE` checkpoint is re-introduced (it needs an uncontended window) — see [project_wal_checkpoint]; that lands with phase 5 cleanup.
- **Node hardening (ops, not in this PR):** raise `fs.inotify.max_user_instances` on the writer node — the inotify exhaustion that wedged prod on 2026-06-11 is mitigated structurally by removing the HTTP-forward churn, but the low default is worth bumping.

## Rollout & rollback

- Feature-flagged by `BUGBARN_REDIS_QUEUE_URL`. Empty → exact current behavior. Set → Redis path.
- Roll out testing → staging → prod via the normal pipeline. Rollback = unset the env var (readers fall back to HTTP forwarding) + redeploy.

## Testing

- Queue: unit tests against `miniredis` (publish/consume/len, retry connect).
- Consumer: fake repo, assert batches drained, mutex held, idempotency on retry.
- Producer: spool → publish, cursor advances only on success, Redis-down accumulates spool.
- Integration: reader+writer+miniredis end-to-end event and log ingest.

## Risks / open questions

1. **At-least-once on the BRPOP hop** — use `BLMOVE` to a processing list (decide in Phase 2).
2. **Redis as a new SPOF** for ingest — mitigated by reader spool anchor; Redis AOF persistence; `NewRedisQueueWithRetry`.
3. **Extra moving part** (Redis pod) — acceptable; spanbarn/funnelbarn already run this.
4. Dashboard mutations remain sync-HTTP — fine (low volume), but means the writer still serves some HTTP writes; keep them under the write mutex too.

[project_wal_checkpoint]: ../../.claude memory — single-writer serialization is what makes the WAL TRUNCATE checkpoint viable.
