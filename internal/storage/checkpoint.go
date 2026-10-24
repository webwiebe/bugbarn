package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DefaultCheckpointInterval is how often the writer TRUNCATE-checkpoints the WAL.
const DefaultCheckpointInterval = 60 * time.Second

// checkpointRetryInterval is how long to wait before retrying a checkpoint that
// came back busy because a reader snapshot blocked WAL backfill.
const checkpointRetryInterval = 5 * time.Second

// checkpointTickBudget bounds how long one tick may keep retrying. Without it a
// tick that keeps seeing busy=1 never yields — the reader pods hold snapshots on
// the shared PVC continuously, so busy=1 can persist indefinitely and the loop
// reaches for the single MaxOpenConns(1) write connection every
// checkpointRetryInterval forever, competing with ingestion for it (#166).
//
// Sized against the 60s default interval so a tick can occupy at most ~1/6 of
// it, and against busy_timeout(10000): an attempt that has to wait on the write
// lock can burn the whole budget on its own, which is fine — giving up and
// letting the next tick retry costs only a slightly larger WAL for one interval.
const checkpointTickBudget = 10 * time.Second

// RunPeriodicCheckpoint blocks until ctx is canceled, issuing a TRUNCATE WAL
// checkpoint on every tick.
//
// This loop is the SOLE checkpointer. sqliteDSN sets wal_autocheckpoint(0), so
// without it nothing ever truncates the WAL and it grows without bound.
//
// It must reach TRUNCATE regularly (CheckpointPolicy), not only PASSIVE. A PASSIVE checkpoint gives up at the first
// reader snapshot boundary, so under sustained read load it silently never
// reclaims anything. This deployment has several reader pods holding read
// snapshots on the same file continuously, so there is no quiet window and
// PASSIVE effectively never completes. That is precisely how production ended
// up 12.8h behind on 2026-07-16 behind a 377MB WAL that could not truncate:
// Litestream was the only checkpointer and it only issues PASSIVE.
//
// The retry is also required: a TRUNCATE checkpoint blocked by a reader
// snapshot reports busy rather than draining, so getting the WAL down under
// read load needs application-level retry, which is what retryInterval does.
//
// That retry is bounded per tick by checkpointTickBudget so it can never hold
// the write connection hostage — an unbounded retry is contention, not
// resilience (#166).
//
// Note the budget is not a precise cap. A blocked attempt can sit in SQLite's
// busy handler for up to busy_timeout (10s, see sqliteDSN) and is NOT
// interruptible by the Go context, so a deadline only takes effect at the next
// retry decision, never mid-pragma. TestCheckpointGivesUpWhenPersistentlyBusy
// measures this: a 300ms deadline still returned after ~10s. Treat the budget
// as "roughly one attempt per tick when the WAL is contended", which is the
// behavior we want anyway.
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
//
// policy decides how hard each tick pushes (see CheckpointPolicy). The zero
// value means DefaultCheckpointPolicy.
func (s *core) RunPeriodicCheckpoint(ctx context.Context, interval time.Duration, policy CheckpointPolicy, log *slog.Logger) {
	if s == nil || s.db == nil {
		return // read-only store: nothing to checkpoint.
	}
	runCheckpointLoop(ctx, s.db, interval, policy, log, checkpointVacuumPages)
}

// RunCheckpointLoop is RunPeriodicCheckpoint for any WAL-mode database opened
// with wal_autocheckpoint(0) on a single write connection. The telemetry
// databases (internal/telemetrydb) use it so each file keeps exactly one
// TRUNCATE checkpointer with the same bounded retry as the main database. They
// keep the legacy policy (TRUNCATE on every tick, 10s busy wait): their writers
// are not behind the check-in path, and their WAL has no tuning knob.
func RunCheckpointLoop(ctx context.Context, db *sql.DB, interval time.Duration, log *slog.Logger) {
	runCheckpointLoop(ctx, db, interval, LegacyCheckpointPolicy(), log, 0)
}

// runCheckpointLoop is RunCheckpointLoop that also returns up to vacuumPages
// freelist pages to the filesystem after each checkpoint. The main database
// passes checkpointVacuumPages; the telemetry databases pass 0 because
// internal/telemetrydb paces its own incremental vacuum.
func runCheckpointLoop(
	ctx context.Context, db *sql.DB, interval time.Duration, policy CheckpointPolicy, log *slog.Logger, vacuumPages int,
) {
	if interval <= 0 {
		interval = DefaultCheckpointInterval
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "wal-checkpoint")
	policy = policy.normalized()

	t := time.NewTicker(interval)
	defer t.Stop()
	st := &checkpointState{}
	for tick := 1; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Per-tick deadline: bound this tick's share of the write
			// connection. Giving up early only leaves the WAL untruncated
			// until the next tick, which then retries from scratch.
			tickCtx, cancel := context.WithTimeout(ctx, checkpointTickBudget)
			checkpointTick(tickCtx, db, tick, st, policy, log)
			reportWALSize(policy, log)
			if vacuumPages > 0 {
				incrementalVacuum(tickCtx, db, vacuumPages, log)
			}
			cancel()
		}
	}
}

// Defaults for CheckpointPolicy. Production measured TRUNCATE holding the write
// connection for 12 to 28 s under reader snapshots (bugbarn#236), which failed
// the check-ins forwarded to the writer in that window.
const (
	defaultTruncateEvery       = 10
	defaultTruncateBusyTimeout = time.Second
	// legacyTruncateBusyTimeout is the connection's own busy_timeout (sqliteDSN).
	legacyTruncateBusyTimeout = 10 * time.Second
)

// CheckpointPolicy tunes the periodic checkpoint without a rebuild
// (BUGBARN_WAL_TRUNCATE_EVERY, BUGBARN_WAL_TRUNCATE_BUSY_TIMEOUT_MS).
//
// Most ticks run a PASSIVE checkpoint, which never waits on a reader and so
// never holds the write connection for more than the copy itself. Every
// TruncateEvery-th tick runs TRUNCATE, which is what resets the WAL file under
// the continuous reader snapshots a PASSIVE checkpoint cannot get past
// (production was 12.8h behind on a 377MB WAL in July 2026 for want of it). A
// TRUNCATE attempt waits for readers at most TruncateBusyTimeout, so the stall it
// causes is bounded by that, not by the connection's 10s busy_timeout.
//
// LegacyCheckpointPolicy (TruncateEvery 1, TruncateBusyTimeout 10s) reproduces
// the behavior before bugbarn#236: TRUNCATE on every tick, waiting up to 10s.
type CheckpointPolicy struct {
	// TruncateEvery makes every Nth tick a TRUNCATE checkpoint; 1 means every tick.
	TruncateEvery int
	// TruncateBusyTimeout bounds how long one TRUNCATE attempt waits for readers.
	TruncateBusyTimeout time.Duration
	// WALSize reports the WAL file size in bytes after each tick. Nil skips the
	// size report (the telemetry databases). Tests inject a fake.
	WALSize func() (int64, error)
	// WarnBytes is the WAL size above which the report logs at Error, which
	// selflog files as an issue in BugBarn itself (BUGBARN_WAL_WARN_BYTES).
	WarnBytes int64
}

// defaultWALWarnBytes sits below the 377MB WAL of the 2026-07-16 incident.
const defaultWALWarnBytes = 256 << 20

// WALFileSize returns a WALSize source that stats dbPath's -wal file; a missing
// file is 0 bytes (the WAL was just truncated).
func WALFileSize(dbPath string) func() (int64, error) {
	return func() (int64, error) {
		fi, err := os.Stat(dbPath + "-wal")
		if os.IsNotExist(err) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
}

// reportWALSize logs the WAL size after a tick: Info normally, Error once it
// passes WarnBytes so a WAL that stops truncating files an issue without anyone
// watching the file by hand.
func reportWALSize(policy CheckpointPolicy, log *slog.Logger) {
	if policy.WALSize == nil {
		return
	}
	size, err := policy.WALSize()
	if err != nil {
		log.Warn("wal size unreadable", "error", err)
		return
	}
	warn := policy.WarnBytes
	if warn <= 0 {
		warn = defaultWALWarnBytes
	}
	if size > warn {
		log.Error("wal file above warn threshold", "wal_bytes", size, "warn_bytes", warn)
		return
	}
	log.Info("wal size", "wal_bytes", size)
}

// DefaultCheckpointPolicy is TRUNCATE every 10th tick with a 1s reader wait.
func DefaultCheckpointPolicy() CheckpointPolicy {
	return CheckpointPolicy{TruncateEvery: defaultTruncateEvery, TruncateBusyTimeout: defaultTruncateBusyTimeout}
}

// LegacyCheckpointPolicy is the behavior before bugbarn#236.
func LegacyCheckpointPolicy() CheckpointPolicy {
	return CheckpointPolicy{TruncateEvery: 1, TruncateBusyTimeout: legacyTruncateBusyTimeout}
}

// normalized replaces unset (zero or negative) fields with the defaults.
func (p CheckpointPolicy) normalized() CheckpointPolicy {
	if p.TruncateEvery <= 0 {
		p.TruncateEvery = defaultTruncateEvery
	}
	if p.TruncateBusyTimeout <= 0 {
		p.TruncateBusyTimeout = defaultTruncateBusyTimeout
	}
	return p
}

// checkpointState carries what one tick tells the next.
type checkpointState struct {
	// failedTruncates counts consecutive TRUNCATE ticks that ended with the WAL
	// still pinned by a reader (or an error).
	failedTruncates int
}

// Backstop: after backstopAfterTicks consecutive blocked TRUNCATE ticks with a
// WAL above backstopBytes, one TRUNCATE waits the full backstopWait for readers.
// The short per-tick wait trades WAL growth for write availability; the backstop
// caps the growth. With the default policy a TRUNCATE tick comes every 10th
// interval, so 3 failures means roughly half an hour of a pinned WAL.
//
// Variables so the tests can shrink them.
var (
	backstopAfterTicks       = 3
	backstopBytes      int64 = 64 << 20
	backstopWait             = 15 * time.Second
)

// checkpointTick runs one tick of the loop: PASSIVE, or TRUNCATE with the bounded
// retry of CheckpointDB when tick is a multiple of TruncateEvery. It logs the
// outcome of every tick so a WAL that stops truncating shows in the logs before
// the size alarm fires.
func checkpointTick(
	ctx context.Context, db *sql.DB, tick int, st *checkpointState, policy CheckpointPolicy, log *slog.Logger,
) {
	policy = policy.normalized()
	if tick%policy.TruncateEvery != 0 {
		checkpointPassive(ctx, db, log)
		return
	}
	truncated, attempts, walFrames, checkpointed := truncateWithRetry(ctx, db, policy, log)
	result := "blocked"
	if truncated {
		result = "truncated"
		st.failedTruncates = 0
	} else {
		st.failedTruncates++
	}
	log.Info("wal checkpoint", "mode", "TRUNCATE", "result", result, "attempts", attempts,
		"wal_frames", walFrames, "checkpointed", checkpointed, "failed_truncate_ticks", st.failedTruncates)
	if !truncated && st.failedTruncates >= backstopAfterTicks {
		backstop(ctx, db, st, policy, log)
	}
}

// checkpointPassive runs and logs one PASSIVE tick.
func checkpointPassive(ctx context.Context, db *sql.DB, log *slog.Logger) {
	_, walFrames, checkpointed, err := checkpointAttempt(ctx, db, "PASSIVE", 0)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("wal checkpoint error", "mode", "PASSIVE", "error", err)
		}
		return
	}
	log.Info("wal checkpoint", "mode", "PASSIVE", "wal_frames", walFrames, "checkpointed", checkpointed)
}

// truncateWithRetry retries a TRUNCATE that a reader blocks, every
// checkpointRetryInterval until the tick's context ends. It reports whether the
// WAL was truncated, how many attempts ran, and the last attempt's frame counts.
func truncateWithRetry(
	ctx context.Context, db *sql.DB, policy CheckpointPolicy, log *slog.Logger,
) (truncated bool, attempts, walFrames, checkpointed int) {
	for {
		busy, frames, done, err := checkpointAttempt(ctx, db, "TRUNCATE", policy.TruncateBusyTimeout)
		attempts++
		walFrames, checkpointed = frames, done
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("wal checkpoint error", "mode", "TRUNCATE", "error", err)
			}
			return false, attempts, walFrames, checkpointed
		}
		if busy == 0 {
			return true, attempts, walFrames, checkpointed
		}
		log.Debug("wal checkpoint blocked by reader, retrying",
			"wal_frames", walFrames, "checkpointed", checkpointed)
		select {
		case <-ctx.Done():
			return false, attempts, walFrames, checkpointed
		case <-time.After(checkpointRetryInterval):
		}
	}
}

// backstop runs one TRUNCATE that waits the full backstopWait for readers, when
// the WAL has grown past backstopBytes. It holds the write connection for up to
// that wait, so it only fires after repeated failures of the short attempt.
func backstop(ctx context.Context, db *sql.DB, st *checkpointState, policy CheckpointPolicy, log *slog.Logger) {
	if policy.WALSize == nil {
		return
	}
	size, err := policy.WALSize()
	if err != nil || size <= backstopBytes {
		return
	}
	log.Warn("wal checkpoint backstop: waiting for readers", "wal_bytes", size,
		"failed_truncate_ticks", st.failedTruncates, "wait", backstopWait.String())
	// The tick's budget is spent by now; the wait is the point of the backstop.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*backstopWait)
	defer cancel()
	busy, walFrames, checkpointed, err := checkpointAttempt(bctx, db, "TRUNCATE", backstopWait)
	if err != nil {
		log.Warn("wal checkpoint backstop error", "error", err)
		return
	}
	result := "blocked"
	if busy == 0 {
		result = "truncated"
		st.failedTruncates = 0
	}
	log.Info("wal checkpoint backstop done", "result", result,
		"wal_frames", walFrames, "checkpointed", checkpointed)
}

// checkpointAttempt runs one `PRAGMA main.wal_checkpoint(mode)` on the write
// connection. A busyTimeout above zero replaces the connection's busy_timeout
// for this attempt only: a TRUNCATE blocked by a reader snapshot waits in
// SQLite's busy handler, which the Go context cannot interrupt, and every other
// write waits for the connection meanwhile.
func checkpointAttempt(
	ctx context.Context, db *sql.DB, mode string, busyTimeout time.Duration,
) (busy, walFrames, checkpointed int, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = conn.Close() }()

	if busyTimeout > 0 {
		var prior int
		if err = conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&prior); err != nil {
			return 0, 0, 0, err
		}
		if _, err = conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA busy_timeout = %d`, busyTimeout.Milliseconds())); err != nil {
			return 0, 0, 0, err
		}
		defer func() {
			// Background context: the attempt's own context may be done, and the
			// pooled connection must not keep the short wait.
			if _, rerr := conn.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA busy_timeout = %d`, prior)); rerr != nil {
				// Discard the connection rather than return it with a wrong timeout.
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
	}

	// Scoped to main on purpose, see CheckpointDB.
	err = conn.QueryRowContext(ctx, `PRAGMA main.wal_checkpoint(`+mode+`)`).Scan(&busy, &walFrames, &checkpointed)
	return busy, walFrames, checkpointed, err
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
	return CheckpointDB(ctx, s.db, retryInterval, log)
}

// CheckpointDB is one TRUNCATE checkpoint of db's main schema with the retry
// semantics described on checkpoint.
func CheckpointDB(ctx context.Context, db *sql.DB, retryInterval time.Duration, log *slog.Logger) int {
	for {
		// wal_checkpoint returns (busy, log, checkpointed): busy=1 means a
		// reader blocked backfill, log = WAL frames, checkpointed = frames
		// moved into the main database.
		//
		// Scoped to main on purpose: an unqualified wal_checkpoint checkpoints
		// EVERY attached database, so with a read-only database ATTACHed (as
		// SnapshotSettings does) it fails the whole call with a disk I/O error.
		var busy, walFrames, checkpointed int
		if err := db.QueryRowContext(ctx, `PRAGMA main.wal_checkpoint(TRUNCATE)`).Scan(&busy, &walFrames, &checkpointed); err != nil {
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
