# Disaster recovery

## What we keep, and what we accept losing

BugBarn takes an **hourly settings-only snapshot** to the shared `barn-backups`
Cloudflare R2 bucket. It is a complete, ready-to-serve `bugbarn.db` containing:

- `project_groups`, `projects`, `project_aliases` — the project registry
- `users` — accounts
- `api_keys` — ingest credentials, so clients keep working after recovery
- `alerts` — alert rules
- `settings` — org settings

Every other table exists but is **empty**. This is a deliberate trade:

> **A total PVC loss costs every event, issue, log and analytics row.**
> Only configuration survives, and the RPO is up to **1 hour**.

That is the accepted design. If you need the bulk data back, this is not the
mechanism — there isn't one.

## Why not continuous replication

Litestream (continuous WAL → S3) was removed. It only ever issues **PASSIVE** WAL
checkpoints, and a PASSIVE checkpoint gives up at the first reader snapshot
boundary. Reader pods hold snapshots on the shared PVC continuously, so there is
no quiet window: the WAL never truncated and grew until every write slowed to a
crawl. That took production down on **2026-06-21** and again on **2026-07-16**
(377 MB WAL, 12.8h ingest backlog).

The writer now bounds its own WAL with `storage.RunPeriodicCheckpoint` —
`PRAGMA main.wal_checkpoint(TRUNCATE)` on an interval, retrying while a reader
blocks backfill. It is the **only** checkpointer: `sqliteDSN` sets
`wal_autocheckpoint(0)`, so if that loop stops running, the WAL grows without
bound. Do not add a second checkpointer; two racing for the write lock just
reproduce the "database is locked" spam.

Tunable via `BUGBARN_WAL_CHECKPOINT_INTERVAL_SECONDS` (default 60) if the WAL
needs to be bounded harder during an incident.

## Where snapshots live

```
s3://barn-backups/bugbarn/settings-snapshots/production/<TIMESTAMP>.db
```

Hourly, newest 14 retained. The CronJob is `bugbarn-settings-snapshot`
(production only — testing and staging are disposable).

## Restoring

The snapshot **is** a working database. There is no restore tool.

1. Find the newest snapshot:
   ```sh
   aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls \
     s3://barn-backups/bugbarn/settings-snapshots/production/
   ```
2. Scale the writer down so nothing holds the database open:
   ```sh
   kubectl -n bugbarn-production scale deploy/bugbarn-writer --replicas=0
   ```
3. Download it into place as `BUGBARN_DB_PATH` (`/var/lib/bugbarn/bugbarn.db`)
   on the `bugbarn-data` PVC. Make sure no `-wal`/`-shm` from the dead database
   are left beside it.
4. Scale the writer back up, then the readers. The writer migrates on open.

## Verifying a snapshot

The job's `snapshot` init container prints per-table row counts. To check by
hand:

```sh
bugbarn db snapshot-settings --src /var/lib/bugbarn/bugbarn.db --out /tmp/settings.db
```

Row counts of `0` for `projects` or `users` mean something is wrong — do not
trust that object as a backup.

## Health signals

- `bugbarn_ingest_wal_size_bytes` — should sawtooth, not climb. A monotonic
  climb means the checkpoint loop is not running or is permanently blocked.
- `bugbarn_write_queue_depth` — the honest backlog signal.
- `bugbarn_ingest_last_event_age_seconds` — **treat with suspicion.** It is
  `now - MAX(received_at)`, so a single recent event resets it to ~0 while an
  arbitrarily large backlog sits behind it. It cannot detect a stall on its own.
