# Feature Specification: Redis Write Queue (CQRS command bus)

**Feature Branch**: `007-redis-write-queue`
**Created**: 2026-06-11
**Spec**: 007-redis-write-queue
**Status**: Partial
**Last verified against the tree**: 2026-10-17 at 70a663f
**Issues**: none found
**Principles touched**: I, V
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
reader (producer)  →  local spool (durability anchor)  →  LPUSH  →  Redis list  →  BLMOVE  →  writer (single consumer)  →  SQLite
```

- Readers `LPUSH` write batches and return immediately; they never block on the writer.
- The single writer takes entries with `BLMOVE` into its processing list and drains at its own pace. No HTTP storm, no backpressure on any request path.
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
        │ writer pod (1):  RedisConsumer BLMOVE                         │
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
- `Publish(ctx, []QueueItem)` → `LPUSH`. `Consume(ctx)` → `BRPOP` with timeout (returns `nil,nil` on timeout so callers loop), used by tests. `Reserve(ctx, key)` → `BLMOVE` into the processing list, `Ack` → `LREM`, `RecoverProcessing` → `LMOVE` back until empty; the writer uses these. `Len(ctx)` for depth metrics.
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
- The reader checks a JSON log body's shape before it spools: a body whose `logs` key is missing, `null` or not an array answers `400` (`logparse.DecodeJSONBody`, bugbarn#263). It used to answer `202`, and the consumer stored nothing from it. NDJSON bodies are spooled as they are.

### Consumer (writer side)
- New `RedisConsumer` (modeled on spanbarn's `RedisWorker.Run`): `Reserve` loop → dispatch by `Kind` (event → `PersistProcessedEvent` pipeline; log → `logs.Insert`).
- Holds the **shared write mutex** for the DB-write phase of each batch.

### Shared write mutex
A single `*sync.Mutex` (or a small `writelock` type) passed to: the Redis consumer, the retention/digest/analytics writers, and the WAL checkpointer. Every code path that opens a write transaction acquires it first. This makes the single SQLite writer connection genuinely single-writer at the application level and lets the TRUNCATE checkpoint run without `SQLITE_BUSY` churn.

### Durability & ordering
- **At-least-once**: the reader's cursor advances only after `LPUSH` success, and from then on the Redis entry is the only copy. The writer takes an entry with `BLMOVE bugbarn:write-queue bugbarn:write-queue:processing:writer RIGHT LEFT 5`, so the pop and the move into its processing list are one step on the server. It acks with `LREM` once every item has a final disposition (stored, held, or dropped as unprocessable). On start, and after a failed read, it moves every entry in the processing list back to the consuming end of the write queue, oldest first. A lost reply or a writer killed between pop and commit therefore loses nothing (bugbarn#257).
- **Over-count on replay**: queue items carry no stable event id on the reader path, so a replay cannot skip events already stored. A writer killed after its commit and before the ack stores that entry again: each event in it gets a second row and its issue's `event_count` one extra. A reader entry holds one item; a requeued tail or an Alertmanager post holds up to 500. A clean shutdown adds none.
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
- **Phase 2 — Writer consumer.** ✅ `internal/ingestproc` — `Processor.PersistRecord` (extracted event pipeline) + `Consumer` (`Reserve` → dispatch → `Ack`). Wired into the writer's `run()` behind the flag; connects in a goroutine so startup never blocks on Redis. The shared write mutex is plumbed (nil for now) and activates when retention + the WAL checkpoint move under it (deferred — see note below).
- **Phase 3 — Reader producer.** ✅ `SpoolForwarder` Redis drain (`NewRedisSpoolForwarder`); `internal/logparse` shared pino parsing; consumer `KindLog` path. Reader uses the Redis spool when configured.
- **Phase 4 — Deploy.** ✅ `redis-queue-deployment.yaml`/`-service.yaml` for staging + production; `BUGBARN_REDIS_QUEUE_URL` on writer + reader; kustomizations updated. Rollout is staged by the pipeline (staging on tag, prod manual).
- **Phase 5 — Cutover & cleanup.** ⏳ *Deferred until the Redis path is validated in production.* Then retire the reader→writer ingest HTTP path (`SpoolForwarder` HTTP send) and the writer's inbound ingest HTTP handler, and fold the file-spool worker's inline pipeline into `ingestproc`. Keep `WriteForwarder` for sync dashboard mutations. The feature flag stays as the rollback path until this happens.

### Decisions made during implementation

- **Consumer delivery:** at first plain `BRPOP` (matches spanbarn), at-most-once on the consumer hop. A lost BRPOP reply or a writer kill between pop and commit dropped the entry, and the reader spool had already advanced past it (bugbarn#257). Replaced by `BLMOVE` to a per-writer processing list with an `LREM` ack and a replay on start: at-least-once, with the over-count under Durability.
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

1. **At-least-once on the consumer hop**: done with `BLMOVE` to a processing list (bugbarn#257). Exactly-once would need a stable id per item and a stored set of seen ids.
2. **Redis as a new SPOF** for ingest — mitigated by reader spool anchor; Redis AOF persistence; `NewRedisQueueWithRetry`.
3. **Extra moving part** (Redis pod) — acceptable; spanbarn/funnelbarn already run this.
4. Dashboard mutations remain sync-HTTP — fine (low volume), but means the writer still serves some HTTP writes; keep them under the write mutex too.

[project_wal_checkpoint]: ../../.claude memory — single-writer serialization is what makes the WAL TRUNCATE checkpoint viable.

---

## Requirements

### Functional Requirements

- **FR-701**: With `BUGBARN_SPOOL_DIR` and `BUGBARN_REDIS_QUEUE_URL` set, a reader MUST append the request to its spool, reply 202 and publish to the Redis list `bugbarn:write-queue`, advancing the spool cursor only after a successful publish. Issues: none found. Tests: `internal/api/spool_forwarder_redis_test.go::TestRedisSpoolForwarderPublishes`.
- **FR-702**: The writer MUST drain the queue with a single consumer and persist events through the shared pipeline. Issues: none found. Tests: `internal/ingestproc/ingestproc_test.go::TestConsumerDrainsEventQueue`.
- **FR-703**: Log items MUST flow through the same queue and consumer. Issues: none found. Tests: `internal/ingestproc/ingestproc_test.go::TestConsumerDrainsLogQueue`.
- **FR-704**: With `BUGBARN_REDIS_QUEUE_URL` empty, readers MUST fall back to the HTTP forwarding path. Issues: none found. Tests: `internal/api/server_forwarding_test.go::TestWriteForwarder_AuthenticatedPOSTForwarded`.
- **FR-705**: Dashboard mutations MUST stay on the synchronous `WriteForwarder`. Issues: none found. Tests: `internal/api/server_forwarding_test.go::TestWriteForwarder_DeleteForwarded`.
- **FR-706**: The consumer MUST push the unfinished tail of a batch back to the queue on shutdown. Issues: none found. Tests: `internal/ingestproc/ingestproc_test.go::TestConsumerRequeuesOnShutdown`.
- **FR-710**: The writer MUST hold each entry in its processing list from the pop until the entry's items have a final disposition, and MUST move the processing list back to the write queue on start. A writer killed between pop and commit loses nothing. Issues: bugbarn#257. Tests: `internal/ingestproc/consumer_replay_test.go::TestWriterKilledBetweenPopAndCommitLosesNothing`, `::TestConsumerAcksAStoredEntry`, `internal/queue/processing_test.go::TestRecoverProcessingReplaysOldestFirst`, `::TestReserveDropsAnUndecodableEntry`.
- **FR-707**: Events for an unapproved project MUST be held and replayed on approval. Issues: none found. Tests: `internal/ingestproc/held_test.go::TestPersistRecordHeldForPendingProject`, `::TestReplayHeldEventsOnApproval`.
- **FR-708**: Large publishes MUST be split into bounded batches. Issues: none found. Tests: `internal/queue/queue_test.go::TestPublishBatchesLargeInput`.
- **FR-709**: The producer MUST NOT block on the writer. [inferred] A Redis outage backs the reader spool up.

### Non-Functional Requirements

- **NFR-701**: The consumer hop is at-least-once. A writer killed after its commit and before its ack stores that entry a second time, and the issue's `event_count` over-counts by one per event in it. Issues: bugbarn#257. Tests: `internal/ingestproc/consumer_replay_test.go::TestWriterKilledBeforeAckStoresTheEntryTwice`.

---

## Drift

| Claim | Source | Tree |
|---|---|---|
| Phase 5 cutover is pending production validation | Header, phase plan | The Redis path runs in production and staging (`deploy/k8s/production/writer-deployment.yaml`); `internal/ingest/CLAUDE.md` says the HTTP drain and the legacy file-spool worker still exist until phase 5 |
| Rollout is staged by the pipeline, "prod manual" | Phase 4 | Production deploys automatically after staging (`.woodpecker/deploy-production.yaml`) |
| Retention and the WAL checkpoint run under the shared write mutex | Architecture | The consumer passes a nil mutex; writes serialise through the single write connection (`internal/storage/CLAUDE.md`) |
| The WAL checkpoint is re-introduced with phase 5 | Decisions | The writer runs `RunPeriodicCheckpoint` (`internal/sqlitedb/checkpoint.go`) since Litestream was removed |
| The periodic `wal_checkpoint(TRUNCATE)` gets clean windows | Header, Architecture | It does not: readers pin snapshots, and a TRUNCATE held the write connection for 12 to 28s (bugbarn#236). Ticks are now PASSIVE except every `BUGBARN_WAL_TRUNCATE_EVERY`-th (default 10), whose reader wait is `BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS` (default 1000) (`internal/sqlitedb/checkpoint.go`) |
| The queue is the only reader-to-writer ingest path | Solution | With no spool dir the reader proxies synchronously; with a spool dir and no Redis URL it POSTs to the writer |
| The spool worker and the Redis consumer persist through separate copies of the pipeline until phase 5 | `internal/ingest/CLAUDE.md` before this change | Both call `ingestproc.Processor.PersistRecord`; only delivery policy differs. Behavior changes for the spool path: the testing monolith holds events of an unknown project as pending unless `BUGBARN_AUTO_APPROVE_PROJECTS` is on (it used to create the project active); a failure to hold an event is a transient retry when the database is busy and a counted failure otherwise (a permanent hold failure on the Redis path now drops at once instead of after 5 retries); a retry logs at WARN and only the dead letter logs at ERROR; releases are persisted by `PersistRelease` and keep their project id; `worker-once` replays through the same processor and reports a `held` count |
| Lock contention is recognised by error text | `internal/storage` before this change | `sqlitedb.IsBusy` reads the SQLite result code; `apperr.Busy` maps to 503 with Retry-After in the API; the string match is gone |
| A listener failure in `run` returns at once | `cmd/bugbarn/main.go` before this change | `run` stops the background loops and waits up to 15s for them before it returns the listener error |
| `[project_wal_checkpoint]` link | Last line | Points at a private memory note; not established |

## Proof contract

- [ ] A reader accepts an event with Redis down, and the writer persists it after Redis returns.
- [ ] A log line posted to a reader appears in the writer's log store.
- [ ] Events for an unapproved project appear after the project is approved.

## Sources

- `internal/queue/queue.go`, `internal/ingestproc/`, `internal/api/spool_forwarder*.go`
- `internal/ingest/CLAUDE.md`, `deploy/CLAUDE.md`
