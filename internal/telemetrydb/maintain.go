package telemetrydb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const (
	// DefaultMaintenanceInterval is how often the writer checkpoints the WAL
	// and enforces the size cap.
	DefaultMaintenanceInterval = 60 * time.Second

	// evictStartPct and evictStopPct are the eviction band: start deleting at
	// 90% of the cap and stop at 85%, so eviction runs in occasional bursts
	// instead of on every tick once the file is near its cap.
	evictStartPct = 90
	evictStopPct  = 85

	// evictBatch rows per DELETE keeps each statement short on the single
	// write connection that ingest shares.
	evictBatch = 5000
	// evictPause yields the write connection between batches.
	evictPause = 50 * time.Millisecond
	// vacuumStep pages per incremental_vacuum call, paced like the deletes.
	vacuumStep = 2048
	// passBudget bounds one maintenance pass so a huge eviction spreads over
	// several ticks rather than holding the write connection for minutes.
	passBudget = 20 * time.Second
	// checkpointRetry mirrors the main database's retry interval.
	checkpointRetry = 5 * time.Second
)

// RunMaintenance blocks until ctx is canceled. Each tick it TRUNCATE-
// checkpoints the WAL (it is this file's sole checkpointer, as
// storage.RunPeriodicCheckpoint is the main file's) and enforces the size cap.
func (d *DB) RunMaintenance(ctx context.Context, interval time.Duration, log *slog.Logger) {
	if d.write == nil {
		return
	}
	if interval <= 0 {
		interval = DefaultMaintenanceInterval
	}
	log = log.With("component", "telemetrydb", "db", d.spec.Name)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.maintain(ctx, log)
		}
	}
}

func (d *DB) maintain(ctx context.Context, log *slog.Logger) {
	passCtx, cancel := context.WithTimeout(ctx, passBudget)
	defer cancel()
	storage.CheckpointDB(passCtx, d.write, checkpointRetry, log)
	if err := d.EnforceCap(passCtx); err != nil && ctx.Err() == nil {
		log.Warn("telemetry size cap enforcement failed", "error", err)
	}
}

// FinalCheckpoint folds the WAL into the file on clean shutdown.
func (d *DB) FinalCheckpoint(log *slog.Logger) {
	if d.write == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storage.CheckpointDB(ctx, d.write, 0, log)
}

// UsedBytes is the space the file's live pages and its WAL take up. Free
// pages are excluded: they are reusable without growing the file, and
// incremental vacuum returns them to the filesystem.
func (d *DB) UsedBytes(ctx context.Context) (int64, error) {
	var pages, free, size int64
	db := d.write
	if db == nil {
		db = d.read
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	used := (pages - free) * size
	if st, err := os.Stat(d.path + "-wal"); err == nil {
		used += st.Size()
	}
	return used, nil
}

// EnforceCap evicts the oldest rows of the bulk table while the file is over
// the eviction band, returns freed pages to the filesystem, and updates Full.
func (d *DB) EnforceCap(ctx context.Context) error {
	if d.maxBytes <= 0 || d.write == nil {
		return nil
	}
	used, err := d.UsedBytes(ctx)
	if err != nil {
		return err
	}
	d.full.Store(used >= d.maxBytes)
	if used < d.maxBytes*evictStartPct/100 {
		return nil
	}
	for used > d.maxBytes*evictStopPct/100 {
		n, err := d.evictOldest(ctx)
		if err != nil {
			return err
		}
		// Deletes land in the WAL first. Without a checkpoint here the WAL
		// grows by every batch, used never drops, and the loop would empty
		// the table chasing a number that deletes cannot lower.
		d.checkpoint(ctx)
		if used, err = d.UsedBytes(ctx); err != nil {
			return err
		}
		d.full.Store(used >= d.maxBytes)
		if n == 0 || !pause(ctx) {
			break
		}
	}
	err = d.vacuum(ctx)
	d.checkpoint(ctx)
	return err
}

// checkpoint is a single TRUNCATE attempt; a reader holding a snapshot only
// delays it to the next attempt.
func (d *DB) checkpoint(ctx context.Context) {
	storage.CheckpointDB(ctx, d.write, 0, slog.New(slog.DiscardHandler))
}

// evictOldest deletes up to evictBatch rows with the lowest evictCol. Both
// subqueries walk an index on evictCol (the primary key for security_logs,
// idx_samples_1m_ts for samples_1m), so the statement never scans the table.
func (d *DB) evictOldest(ctx context.Context) (int64, error) {
	t, c := d.spec.evictTable, d.spec.evictCol
	q := fmt.Sprintf(`DELETE FROM %[1]s WHERE %[2]s <= COALESCE(
		(SELECT %[2]s FROM %[1]s ORDER BY %[2]s LIMIT 1 OFFSET %[3]d),
		(SELECT MAX(%[2]s) FROM %[1]s))`, t, c, evictBatch-1)
	res, err := d.write.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("evict %s: %w", t, err)
	}
	return res.RowsAffected()
}

// vacuum returns free pages to the filesystem in small steps.
func (d *DB) vacuum(ctx context.Context) error {
	for {
		var free int64
		if err := d.write.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
			return err
		}
		if free == 0 {
			return nil
		}
		if _, err := d.write.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, vacuumStep)); err != nil {
			return fmt.Errorf("incremental vacuum: %w", err)
		}
		if !pause(ctx) {
			return nil
		}
	}
}

func pause(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(evictPause):
		return true
	}
}
