# storage: the SQLite layer

One SQLite file, one `Store`. `Store` is a facade that embeds a domain store per model
(`IssueStore`, `EventStore`, `LogStore`, ...) over a shared `core` (`types.go`). Services
should depend on a domain store or a narrow interface over one.

## Errors stop here

Wrap every DB error before it leaves this package, with `wrapErr` / `wrapNotFound`
(`errors.go`):

- `sql.ErrNoRows` becomes `apperr.NotFound`
- a UNIQUE violation becomes `apperr.Conflict`
- anything else becomes `apperr.Internal`

Never return `sql.ErrNoRows`, a `*sqlite.Error` or a raw driver error. Services and the API
map on `apperr` codes; a raw error surfaces as a 500. `IsDatabaseLocked` is the one
exported check on driver errors, for retry decisions in the ingest consumers.

This package does not log as a rule. The existing `slog` calls are best-effort side paths
(driver registration, the background fingerprint migration, regression bookkeeping). Do
not add new ones; return an error and let the service log it.

## Connections and roles

- `Open` (single-process and writer): a write pool with `SetMaxOpenConns(1)` and
  `_txlock=immediate`, plus a read pool of 4. Runs goose migrations from
  `migrations/*.sql` (embedded) and starts the background fingerprint migration (see
  `internal/fingerprint/CLAUDE.md`).
- `OpenReadOnly` (reader pods): read pool only, `mode=ro`, no migrations. Write methods
  fail because `db` is nil. Readers share the writer's file on the same PVC.

The single write connection is shared by ingest, retention, log trimming, admin mutations
and the WAL checkpoint. A slow write statement stalls all ingestion. A per-insert
`COUNT(DISTINCT ...)` once cost 46s per event this way; keep hot-path writes to indexed
point lookups.

## WAL

`sqliteDSN` sets `journal_mode(wal)` and `wal_autocheckpoint(0)`. `RunPeriodicCheckpoint`
(`checkpoint.go`, writer only, every `BUGBARN_WAL_CHECKPOINT_INTERVAL_SECONDS`) is the
sole checkpointer and must stay TRUNCATE with a bounded retry per tick. Reader pods hold
snapshots continuously, so a PASSIVE checkpoint never completes. Do not add a second
checkpointer, and keep WAL mode: the readers depend on it.

## Migrations

`migrations/NNNNN_name.sql`, goose format, applied on writer start before the pod is
ready. Production's database is over 10 GB: an index build or table rewrite takes minutes,
and the startupProbe budget (600s) is the ceiling. Measure a new migration against a
realistic copy and state its cost in the PR.

## Query plans over timings

Several indexes exist only so a query can be answered index-only (`00011`, `00012`,
`00015`). Changing a query shape, for example `COUNT(*)` to `SUM(col)` or dropping an
`ORDER BY` column, can silently fall back to a table scan. Tests that guard these assert
on `EXPLAIN QUERY PLAN` output (`COVERING INDEX`); follow that pattern rather than
asserting a duration.

## Tests

Use a real database in `t.TempDir()`; there are no storage mocks. Tests that write
explicit fingerprints use `open(path, false)` so the background migration does not
rewrite them.

`storage` imports `internal/worker`, so `worker` cannot import `storage`. That is why the
Redis consumer pipeline lives in `internal/ingestproc`.
