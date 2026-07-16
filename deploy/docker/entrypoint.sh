#!/bin/sh
set -eu

# Both reader and writer just run the binary.
#
# The writer used to be wrapped in `litestream replicate`, with a
# `litestream restore` on an empty PVC. Litestream is gone: it only ever issued
# PASSIVE WAL checkpoints, and a PASSIVE checkpoint stops at the first reader
# snapshot boundary. With reader pods holding snapshots on the shared PVC
# continuously there is no quiet window, so the WAL never truncated and grew
# until every write slowed to a crawl — production incidents on 2026-06-21 and
# again on 2026-07-16.
#
# The writer now bounds its own WAL via storage.RunPeriodicCheckpoint
# (TRUNCATE, with retry). Disaster recovery is the hourly settings-snapshot
# CronJob to R2 rather than continuous replication: restore by dropping the
# newest snapshot object in as BUGBARN_DB_PATH and starting the pod. See
# docs/deployment/disaster-recovery.md.
exec bugbarn
