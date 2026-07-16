package storage

import (
	"context"
	"log/slog"
	"time"
)

// DefaultCheckpointInterval is how often the writer TRUNCATE-checkpoints the WAL.
const DefaultCheckpointInterval = 60 * time.Second

// checkpointRetryInterval is how long to wait before retrying a checkpoint that
// came back busy because a reader snapshot blocked WAL backfill.
const checkpointRetryInterval = 5 * time.Second

// RunPeriodicCheckpoint blocks until ctx is canceled, issuing a TRUNCATE WAL
// checkpoint on every tick.
//
// This loop is the SOLE checkpointer. sqliteDSN sets wal_autocheckpoint(0), so
// without it nothing ever truncates the WAL and it grows without bound.
//
// It must be TRUNCATE, not PASSIVE. A PASSIVE checkpoint gives up at the first
// reader snapshot boundary, so under sustained read load it silently never
// reclaims anything. This deployment has several reader pods holding read
// snapshots on the same file continuously, so there is no quiet window and
// PASSIVE effectively never completes. That is precisely how production ended
// up 12.8h behind on 2026-07-16 behind a 377MB WAL that could not truncate:
// Litestream was the only checkpointer and it only issues PASSIVE.
//
// The retry is also required. A TRUNCATE checkpoint does NOT wait on
// busy_timeout when a reader blocks WAL backfill — it returns SQLITE_BUSY
// immediately in that phase. busy_timeout only covers write-lock conflicts
// between writers. So draining the WAL under read load needs application-level
// retry, which is what retryInterval does here.
//
// Runs on the same *sql.DB as every other writer: SQLite allows one writer at a
// time at the file level, so a dedicated second connection would only move the
// contention out of Go's pool and into SQLite's lock machinery, where it
// surfaces as immediate SQLITE_BUSY instead of queueing. The write connection is
// capped at MaxOpenConns(1) and bugbarn's write transactions are short, so
// serializing the checkpoint behind them is cheap.
//
// The checkpoint runs unconditionally on every tick. Do not add a "skip while
// the write queue is backed up" gate: spanbarn shipped exactly that and a stale
// backlog kept the gate permanently tripped, so no checkpoint ever ran and the
// WAL bloated to hundreds of MB — which made every write slow, the precise
// opposite of the intent (see spanbarn #117, reverted in #131).
func (s *core) RunPeriodicCheckpoint(ctx context.Context, interval time.Duration, log *slog.Logger) {
	if s == nil || s.db == nil {
		return // read-only store: nothing to checkpoint.
	}
	if interval <= 0 {
		interval = DefaultCheckpointInterval
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "wal-checkpoint")

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkpoint(ctx, checkpointRetryInterval, log)
		}
	}
}

// FinalCheckpoint runs a single checkpoint on a fresh context. Call it after all
// writers have stopped and before Close, so the WAL is merged into the main
// database on a clean shutdown. With wal_autocheckpoint(0), Close does not
// checkpoint on its own.
func (s *core) FinalCheckpoint(log *slog.Logger) {
	if s == nil || s.db == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// retryInterval 0: on shutdown take one attempt and move on rather than
	// blocking the process exit behind a reader that may outlive us.
	s.checkpoint(ctx, 0, log.With("component", "wal-checkpoint"))
}

// checkpoint runs one PRAGMA wal_checkpoint(TRUNCATE) and returns the WAL size
// in frames after the attempt (the pragma's `log` column), or -1 on error.
// When retryInterval > 0 it retries while the pragma reports busy, so the WAL is
// actually reset once the blocking reader releases its snapshot.
func (s *core) checkpoint(ctx context.Context, retryInterval time.Duration, log *slog.Logger) int {
	for {
		// wal_checkpoint returns (busy, log, checkpointed): busy=1 means a
		// reader blocked backfill, log = WAL frames, checkpointed = frames
		// moved into the main database.
		//
		// Scoped to main on purpose: an unqualified wal_checkpoint checkpoints
		// EVERY attached database, so with a read-only database ATTACHed (as
		// SnapshotSettings does) it fails the whole call with a disk I/O error.
		var busy, walFrames, checkpointed int
		if err := s.db.QueryRowContext(ctx, `PRAGMA main.wal_checkpoint(TRUNCATE)`).Scan(&busy, &walFrames, &checkpointed); err != nil {
			if ctx.Err() == nil {
				log.Warn("wal checkpoint error", "error", err)
			}
			return -1
		}
		if busy == 0 {
			return walFrames
		}
		log.Debug("wal checkpoint blocked by reader, retrying",
			"wal_frames", walFrames, "checkpointed", checkpointed)
		if retryInterval == 0 {
			return walFrames
		}
		select {
		case <-ctx.Done():
			return walFrames
		case <-time.After(retryInterval):
		}
	}
}
