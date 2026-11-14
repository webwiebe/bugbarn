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

The generic SQLite plumbing lives in `internal/sqlitedb`, shared with `internal/telemetrydb`
(security.db, metrics.db): driver registration and names, the read-only recovery driver,
`IsReadonlyRecovery` and the WAL checkpoint mechanics that work on a `*sql.DB`. `storage`
keeps what is specific to bugbarn.db: the DSNs, pools, migrations, `RunPeriodicCheckpoint`
and `FinalCheckpoint` as thin methods over `sqlitedb`, and the `CheckpointPolicy`,
`WALFileSize` names that `cmd/bugbarn` uses (aliases in `wal.go`). Put new driver or
checkpoint code that any SQLite file could use in `sqlitedb`.

The single write connection is shared by ingest, retention, log trimming, admin mutations
and the WAL checkpoint. A slow write statement stalls all ingestion. A per-insert
`COUNT(DISTINCT ...)` once cost 46s per event this way; keep hot-path writes to indexed
point lookups.

## WAL

`sqliteDSN` sets `journal_mode(wal)` and `wal_autocheckpoint(0)`. `RunPeriodicCheckpoint`
(`wal.go`, running `sqlitedb.RunPolicyLoop` in `internal/sqlitedb/checkpoint.go`; writer only, every `BUGBARN_WAL_CHECKPOINT_INTERVAL_SECONDS`) is the
sole checkpointer. It runs PASSIVE on most ticks and TRUNCATE every
`BUGBARN_WAL_TRUNCATE_EVERY`-th tick (`CheckpointPolicy`), with a bounded retry per tick.
Reader pods hold snapshots continuously, so PASSIVE alone never resets the WAL file; the
TRUNCATE tick must stay. A TRUNCATE attempt waits for readers at most
`BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS` (default 1s, not the connection's 10s) because it
holds the single write connection while it waits (bugbarn#236). After 3 consecutive
blocked TRUNCATE ticks with a WAL over 64 MiB, one TRUNCATE waits 15s (the backstop), and
every tick logs `wal checkpoint` with its result. Do not add a second
checkpointer, and keep WAL mode: the readers depend on it.

Reader pods mount the volume read-only, so a reader holds the `-shm` file read-only. When
the wal-index header there needs recovery, only the writer can rebuild it, and until its
next write every read on the reader fails with `SQLITE_READONLY_RECOVERY` (264).
`OpenReadOnly` therefore opens its pool on a driver (`sqlitedb.ReadOnlyDriverName`, `internal/sqlitedb/readonly_driver.go`) that retries
every query and prepare on 264 three times (`retryReadonlyRecovery` in `internal/sqlitedb`, 175 ms in all),
counts the outcome in `bugbarn.storage.readonly_recovery` and puts the WAL's size and
age on a span event (#242, #261). SQLITE_READONLY_CANTINIT (1288), a reader that cannot
initialize the wal-index, is the same state and takes the same path (#268), and so is
SQLITE_PROTOCOL (15, "locking protocol"), a reader that lost the wal-index lock race to
the writer (#275). A 264, 1288 or 15 that outlasts the retries becomes `apperr.Unavailable` (503) in `wrapErr`. Do not add per-call retries; the driver covers every read.

`internal/sqlitedb/readonly_recovery_process_test.go` reproduces the state: a reader in a child process,
opened with `readonly_shm=1` (a file mode would not hold, since CI runs the tests as
root), reads once; the test zeroes the header and the reader's
next read gets 264. The reader must be a separate, long-lived process. A reader in the
writer's process shares the writer's shm mapping, and a connection opened after the
header was zeroed does not hit 264.

## Free space

bugbarn.db was created with `auto_vacuum=NONE`, so rows the retention sweep deletes leave
freelist pages and the file never shrinks. `Compact` (`compact.go`) rewrites the file once
in `auto_vacuum=INCREMENTAL` mode; after that the checkpoint loop runs
`PRAGMA incremental_vacuum` on every tick and freed pages go back to the volume. The
writer runs it at start when `BUGBARN_COMPACT_ON_START=true` (testing and staging), and
skips it when live data exceeds `BUGBARN_COMPACT_MAX_LIVE_BYTES` (default 1 GiB), since
VACUUM holds the write connection. The same switch deletes the `.bugbarn.db-litestream`
shadow directory Litestream left behind.

## Project-owned tables

`projectTables` (`project_tables.go`) names every table with a `project_id` column, once, in
delete order (children first) with a merge mode: move, move the rows the target lacks, or
drop with the source. `DeleteProject` and `MergeProjects` both walk it, and
`TestProjectTablesCoverEverySchemaTable` fails when a migration adds a table the list lacks.
A new table with `project_id` gets an entry in the same PR. `api_keys.project_id` has no
ON DELETE action, so a key left behind blocks the delete of its project (#270).

## Migrations

`migrations/NNNNN_name.sql`, goose format, applied on writer start before the pod is
ready. Production's database is over 10 GB: an index build or table rewrite takes minutes,
and the startupProbe budget (600s) is the ceiling. Measure a new migration against a
realistic copy and state its cost in the PR.

## Timestamps are text

Timestamp columns are TEXT, and SQLite compares them as strings. Write every time
parameter and value through `formatTime`: UTC with a nine-digit fraction, so string order
is time order (#253). `time.RFC3339Nano` trims trailing zeros and sorts wrong inside one
second; `time.RFC3339` drops the fraction and sorts wrong against stored fractions. Rows
written before #253 keep the trimmed form and `parseTime` reads both. Against those rows
`<` and `>` stay correct across seconds, and `=` does not match, which is why migration
00026 rewrote `monitors.last_checkin_at` for `MarkMonitorMissed`. A new `=` match on a
timestamp column needs the same rewrite or a match on the parsed time.

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
