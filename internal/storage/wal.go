package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/sqlitedb"
)

// The WAL checkpoint mechanics live in internal/sqlitedb so the telemetry files
// share them. These aliases keep the storage names that cmd/bugbarn uses.

// DefaultCheckpointInterval is how often the writer checkpoints the WAL.
const DefaultCheckpointInterval = sqlitedb.DefaultCheckpointInterval

// CheckpointPolicy tunes the periodic checkpoint; see sqlitedb.CheckpointPolicy.
type CheckpointPolicy = sqlitedb.CheckpointPolicy

// WALFileSize returns a CheckpointPolicy.WALSize source for dbPath's -wal file.
func WALFileSize(dbPath string) func() (int64, error) { return sqlitedb.WALFileSize(dbPath) }

// DefaultCheckpointPolicy is TRUNCATE every 10th tick with a 1s reader wait.
func DefaultCheckpointPolicy() CheckpointPolicy { return sqlitedb.DefaultCheckpointPolicy() }

// LegacyCheckpointPolicy is the behavior before bugbarn#236.
func LegacyCheckpointPolicy() CheckpointPolicy { return sqlitedb.LegacyCheckpointPolicy() }

// RunPeriodicCheckpoint blocks until ctx is canceled, checkpointing bugbarn.db's
// WAL on every tick and returning freelist pages to the filesystem afterwards.
// It is the sole checkpointer of the file (see sqliteDSN and sqlitedb.RunPolicyLoop).
// A read-only store has no write connection and returns immediately.
func (s *core) RunPeriodicCheckpoint(ctx context.Context, interval time.Duration, policy CheckpointPolicy, log *slog.Logger) {
	if s == nil || s.db == nil {
		return // read-only store: nothing to checkpoint.
	}
	sqlitedb.RunPolicyLoop(ctx, s.db, interval, policy, log, checkpointVacuumPages)
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

// checkpoint runs one TRUNCATE checkpoint and returns the WAL size in frames
// afterwards, or -1 on error; see sqlitedb.CheckpointDB.
func (s *core) checkpoint(ctx context.Context, retryInterval time.Duration, log *slog.Logger) int {
	return sqlitedb.CheckpointDB(ctx, s.db, retryInterval, log)
}
