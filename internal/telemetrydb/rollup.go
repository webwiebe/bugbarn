package telemetrydb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

const (
	hourMs = int64(time.Hour / time.Millisecond)

	// rollupGrace is how long after an hour ends the rollup waits before it
	// treats the hour as finished, so samples delivered a few minutes late
	// still land in it. Samples that arrive later than this are kept in
	// samples_1m but are missing from that hour's rollup.
	rollupGrace = 10 * time.Minute
	// rollupMaxHours bounds one pass. One hour is one INSERT ... SELECT over
	// that hour's minute rows (hosts x metrics x 60), so a backlog of days
	// spreads over several ticks.
	rollupMaxHours = 24

	// retentionBatch rows per DELETE, matching the eviction batch size.
	retentionBatch = evictBatch

	rollupCursorKey = "rollup_1h_next"
)

// Retention is how long metrics.db keeps each resolution. Hosts that have
// not reported for the Hourly window are dropped as well. A zero or negative
// window disables that part of the sweep.
type Retention struct {
	Raw    time.Duration
	Hourly time.Duration
}

// RunMetricsJobs blocks until ctx is canceled. Each tick it rolls finished
// hours of samples_1m into samples_1h and then applies retention. Writer only:
// it returns at once on a read-only DB.
func (d *DB) RunMetricsJobs(ctx context.Context, interval time.Duration, ret Retention, log *slog.Logger) {
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
			d.metricsPass(ctx, ret, log)
		}
	}
}

func (d *DB) metricsPass(ctx context.Context, ret Retention, log *slog.Logger) {
	passCtx, cancel := context.WithTimeout(ctx, passBudget)
	defer cancel()
	now := time.Now()
	if _, err := d.Rollup(passCtx, now); err != nil && ctx.Err() == nil {
		log.Warn("metrics hourly rollup failed", "error", err)
	}
	if err := d.ApplyRetention(passCtx, ret, now); err != nil && ctx.Err() == nil {
		log.Warn("metrics retention failed", "error", err)
	}
}

// Rollup aggregates every finished hour of samples_1m that has not been
// rolled up yet into samples_1h (min, avg, max and sample count per host and
// metric), up to rollupMaxHours per call. It returns the number of hours
// written. Each hour commits together with the cursor, and the insert
// replaces an existing row, so running it again never double-counts.
func (d *DB) Rollup(ctx context.Context, now time.Time) (int, error) {
	return d.rollup(ctx, now, rollupMaxHours)
}

func (d *DB) rollup(ctx context.Context, now time.Time, maxHours int) (int, error) {
	if d.write == nil {
		return 0, nil
	}
	limit := now.Add(-rollupGrace).UnixMilli()
	done := 0
	for done < maxHours {
		ok, err := d.rollupNextHour(ctx, limit)
		if err != nil {
			return done, err
		}
		if !ok {
			break
		}
		done++
		if done < maxHours && !pause(ctx) {
			break
		}
	}
	return done, nil
}

// rollupNextHour rolls up the first hour at or after the cursor that has
// samples and ended before limit. It reports false when there is none.
// Hours without samples are skipped by jumping to the next sample.
func (d *DB) rollupNextHour(ctx context.Context, limit int64) (bool, error) {
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return false, apperr.Internal("begin rollup", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	start, ok, err := nextRollupHour(ctx, tx)
	if err != nil || !ok || start+hourMs > limit {
		return false, err
	}
	end := start + hourMs
	if _, err := tx.ExecContext(ctx, rollupHourSQL, start, start, end); err != nil {
		return false, apperr.Internal("roll up hour", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, rollupCursorKey, end); err != nil {
		return false, apperr.Internal("save rollup cursor", err)
	}
	if err := tx.Commit(); err != nil {
		return false, apperr.Internal("commit rollup", err)
	}
	return true, nil
}

// rollupHourSQL aggregates one hour. INDEXED BY pins the range scan on
// idx_samples_1m_ts: without statistics the planner may prefer walking the
// whole primary key to avoid the GROUP BY sort, which would read every row in
// the table to roll up one hour.
const rollupHourSQL = `INSERT INTO samples_1h (host, metric, ts, min, avg, max, n)
	SELECT host, metric, ?, MIN(value), AVG(value), MAX(value), COUNT(*)
	FROM samples_1m INDEXED BY idx_samples_1m_ts
	WHERE ts >= ? AND ts < ?
	GROUP BY host, metric
	ON CONFLICT(host, metric, ts) DO UPDATE SET
		min = excluded.min, avg = excluded.avg, max = excluded.max, n = excluded.n`

// nextRollupHour returns the start of the hour holding the first sample at or
// after the cursor. Both lookups are point reads: the meta primary key and
// the low end of idx_samples_1m_ts.
func nextRollupHour(ctx context.Context, tx *sql.Tx) (int64, bool, error) {
	var cursor int64
	err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, rollupCursorKey).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, apperr.Internal("read rollup cursor", err)
	}
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(ts) FROM samples_1m WHERE ts >= ?`, cursor).Scan(&first); err != nil {
		return 0, false, apperr.Internal("find next rollup hour", err)
	}
	if !first.Valid {
		return 0, false, nil
	}
	return first.Int64 - first.Int64%hourMs, true, nil
}

// ApplyRetention deletes minute samples older than ret.Raw, hourly rollups
// older than ret.Hourly, and hosts not seen within ret.Hourly. Deletes run in
// batches of retentionBatch rows with a pause between them, and stop early
// when ctx ends; the next pass picks up where this one stopped.
func (d *DB) ApplyRetention(ctx context.Context, ret Retention, now time.Time) error {
	if d.write == nil {
		return nil
	}
	if ret.Raw > 0 {
		if err := d.deleteOlder(ctx, "samples_1m", now.Add(-ret.Raw)); err != nil {
			return err
		}
	}
	if ret.Hourly <= 0 {
		return nil
	}
	if err := d.deleteOlder(ctx, "samples_1h", now.Add(-ret.Hourly)); err != nil {
		return err
	}
	if _, err := d.write.ExecContext(ctx, `DELETE FROM hosts WHERE last_seen < ?`,
		now.Add(-ret.Hourly).UnixMilli()); err != nil {
		return apperr.Internal("expire hosts", err)
	}
	return nil
}

// deleteOlder removes the rows of a samples table with ts before cutoff. The
// subquery walks the table's ts index from the low end and picks at most one
// batch of primary keys, so each DELETE touches a bounded number of rows.
func (d *DB) deleteOlder(ctx context.Context, table string, cutoff time.Time) error {
	q := fmt.Sprintf(`DELETE FROM %[1]s WHERE (host, metric, ts) IN (
		SELECT host, metric, ts FROM %[1]s INDEXED BY idx_%[1]s_ts WHERE ts < ? ORDER BY ts LIMIT %[2]d)`,
		table, retentionBatch)
	for {
		res, err := d.write.ExecContext(ctx, q, cutoff.UnixMilli())
		if err != nil {
			return apperr.Internal("expire "+table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return apperr.Internal("expire "+table, err)
		}
		if n < retentionBatch || !pause(ctx) {
			return nil
		}
	}
}
