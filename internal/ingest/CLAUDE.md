# The ingest write path

Covers `ingest`, `ingestproc`, `ingesthealth`, `ingestresp`, `queue`, `spool`, `worker`,
`mutqueue` and `logparse`. Wiring is in `cmd/bugbarn/main.go` (single-process and writer)
and `cmd/bugbarn/reader.go` (reader).

## Roles

`BUGBARN_MODE` picks the role (`internal/config`):

- `""` (single-process) and `"writer"` wire identically: read-write store, file spool,
  legacy spool worker, Redis consumer when `BUGBARN_REDIS_QUEUE_URL` is set, retention,
  digest, analytics rollup, WAL checkpoint.
- `"reader"` opens the store read-only and never writes SQLite. `BUGBARN_WRITER_URL` is
  required. Every non-GET request it cannot answer from the read store (issue mutations,
  releases, login/logout, session writes) is proxied to the writer by `api.WriteForwarder`.

Production runs one writer and several readers on the same PVC; see `deploy/CLAUDE.md`.

## Reader to writer handoff

Events, logs, Alertmanager webhooks and analytics collect arrive at a reader. What
happens next is fixed at startup:

| `BUGBARN_SPOOL_DIR` set? | `BUGBARN_REDIS_QUEUE_URL` set? | Path |
|---|---|---|
| no | any | Synchronous proxy to the writer (`WriteForwarder`, 30s). A writer outage returns a plain 502. |
| yes | no | Append to the reader's `forward.ndjson`, reply 202, drain by POSTing to `BUGBARN_WRITER_URL` (`api.SpoolForwarder`). |
| yes | yes | Same append and 202, drain by `LPUSH` to the Redis list `bugbarn:write-queue` (`queue.Publish`). |

The reader only gets a spool when `BUGBARN_SPOOL_DIR` is set explicitly in the
environment; the config default is not writable in the container.

The reader's disk spool is the durability anchor in both drained modes. Its cursor
advances only after a successful publish or POST, so a Redis or writer outage backs the
spool up without loss. There is no runtime switch between Redis and HTTP.

In Redis mode only `/api/v1/events` and `/api/v1/logs` map to queue kinds. Alertmanager
envelopes are expanded to one event per alert before publishing; analytics collect is
dropped with a warning.

## Writer side

Two consumers persist events, and both go through `worker.ProcessRecord`
(normalize, scrub, fingerprint), symbolication and `storage.PersistProcessedEvent`:

- **Legacy file-spool worker** (`cmd/bugbarn/worker.go`, 1s tick). Drains
  `internal/mutqueue` first, then `ingest.ndjson` from its cursor. Used by direct writer
  ingest, HTTP-mode reader replays, Alertmanager and `POST /api/v1/releases`. Transient
  lock errors retry forever; other failures retry 3 times, then go to
  `deadletter.ndjson`. It creates projects with `EnsureProject` and never holds events.
- **Redis consumer** (`internal/ingestproc`, spec 007). `BRPOP` batches, `PersistRecord`
  with up to 5 retries on transient errors. Delivery is at-most-once: an item is popped
  before it is persisted, and a non-transient failure drops it. On shutdown the
  unfinished tail is pushed back. Honours pending projects: with
  `BUGBARN_AUTO_APPROVE_PROJECTS` off, events for an unapproved project are stored via
  `HoldEvent` and replayed by `ingestproc.Replayer` on approval.

The two copies of the pipeline differ (project creation, held events, transient-error
detection). They are meant to converge in spec 007 phase 5; until then a pipeline change
usually has to land in both. The pipeline lives in `ingestproc` because `storage` imports
`worker`, so `worker` cannot hold anything that needs `storage`.

Direct log ingest on the writer skips the spool and inserts synchronously.
`internal/logparse` parses pino-style bodies for both the HTTP endpoint and the consumer.

`internal/mutqueue` (writer only) is the fallback for admin issue mutations: the handler
tries the store for 5s, then appends to `mutations.ndjson` and returns 202
`{"queued":true}`.

## Response contract (`ingestresp`)

SDKs decide whether to retry from the status and body alone:

- 202 `{"accepted":true,"ingestId"}` (empty id on readers)
- permanent, do not retry: 400 `malformed_payload`, 401 `unauthorized`, 413 `payload_too_large`
- transient, retry: 429 `spool_full`, 503 `ingest_unavailable`, with `Retry-After: 1`

`ingest.Handler` runs `normalize.Validate` before replying, so a 202 means the payload
will parse. Known exceptions still answer outside the contract: route-level API-key checks
(plain 401), a reader log without a project (plain 400) and `WriteForwarder` (plain 502).
Use `ingestresp` for any new ingest response.

## Stall detection (`ingesthealth`)

Runs in every role. Every 60s it samples `MAX(received_at)` of events, Redis `LLEN`
(entries are batches of up to 500 items) and the WAL file size:

- stale last event with a known queue depth of 0 is idle and healthy
- stale with depth > 0, or stale with no queue (monolith), is unhealthy
- depth above 50,000 is always unhealthy
- WAL size only logs a warning

Unhealthy makes `/api/v1/health?detail=true` return 503, logs a throttled ERROR, and
notifies out of band (`BUGBARN_INGEST_ALERT_WEBHOOK_URL`, `BUGBARN_INGEST_ALERT_EMAIL`).
The out-of-band path exists because self-reporting goes through the pipeline that has
stalled. Notifier failures log at WARN for the same reason.

One recent event hides a large backlog from the staleness check. Judge a backlog by
queue depth.
