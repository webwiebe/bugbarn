package storage

import (
	"context"
	"log/slog"
	"time"
)

// RunPeriodicCheckpoint blocks until ctx is cancelled, issuing a WAL TRUNCATE
// checkpoint on each tick. When a checkpoint returns busy=1 (a reader snapshot
// blocks full WAL backfill) it retries every retryInterval until the readers
// release or the next full-interval tick arrives.
//
// The checkpoint MUST run on the writer connection (s.db), not a second
// connection or an out-of-process tool. SQLite permits one writer at the file
// level; a separate connection upgrades its deferred transaction to a write
// lock and returns SQLITE_BUSY immediately, bypassing busy_timeout. That is
// exactly why Litestream's out-of-process PASSIVE checkpoint logs
// "checkpoint: database is locked" every second under write load. Serialising
// the checkpoint through s.db means it simply queues in Go's pool behind the
// other (tiny) write transactions and always gets a clean window.
//
// Litestream stays active for S3 replication. Its WAL-level reader lock holds a
// snapshot at the last frame it has shipped, so this TRUNCATE checkpoint can
// never reclaim frames Litestream has not yet confirmed — SQLite blocks the
// backfill at that boundary (busy=1) and we retry. Dual checkpointing is safe.
func (s *Store) RunPeriodicCheckpoint(ctx context.Context, interval time.Duration, log *slog.Logger) {
	if s == nil || s.db == nil {
		return
	}
	retryInterval := 5 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkpoint(ctx, retryInterval, log)
		}
	}
}

// FinalCheckpoint runs one WAL TRUNCATE checkpoint on a fresh context. Call
// after all writers have stopped (worker drained, HTTP server shut down) and
// before Close(), so the WAL is merged into the main file on clean shutdown —
// with wal_autocheckpoint(0), Close() does not checkpoint automatically.
func (s *Store) FinalCheckpoint(log *slog.Logger) {
	if s == nil || s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.checkpoint(ctx, 0, log)
}

func (s *Store) checkpoint(ctx context.Context, retryInterval time.Duration, log *slog.Logger) {
	for {
		var busy, walFrames, checkpointed int
		if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &walFrames, &checkpointed); err != nil {
			if ctx.Err() == nil && log != nil {
				log.Warn("wal checkpoint error", "error", err)
			}
			return
		}
		if busy == 0 {
			return
		}
		// busy=1: a reader snapshot blocks full WAL backfill. TRUNCATE does not
		// honour busy_timeout in this phase (that only covers write-lock conflicts),
		// so retry at the application level until the readers release or ctx ends.
		if log != nil {
			log.Debug("wal checkpoint blocked by reader, retrying", "wal_frames", walFrames, "checkpointed", checkpointed)
		}
		if retryInterval == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}
}
