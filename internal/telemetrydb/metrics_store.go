package telemetrydb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
)

// InsertMetrics upserts per-minute samples and host info in one transaction.
// A repeated (host, metric, minute) keeps the latest value. When the file is
// at its cap the samples are skipped and ErrFull returned, but host info is
// still written: it is one row per host, and the heartbeat rule reads it.
func (d *DB) InsertMetrics(ctx context.Context, samples []hostmetrics.Sample, hosts []hostmetrics.HostInfo) error {
	if len(samples) == 0 && len(hosts) == 0 {
		return nil
	}
	full := d.Full()
	if full {
		samples = nil
	}
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return apperr.Internal("begin metrics insert", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for start := 0; start < len(samples); start += insertChunk {
		end := min(start+insertChunk, len(samples))
		if err := insertSampleChunk(ctx, tx, samples[start:end]); err != nil {
			return apperr.Internal("insert samples", err)
		}
	}
	for _, h := range hosts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO hosts (host, cores, last_seen) VALUES (?, ?, ?)
			ON CONFLICT(host) DO UPDATE SET
				cores = CASE WHEN excluded.cores > 0 THEN excluded.cores ELSE hosts.cores END,
				last_seen = MAX(hosts.last_seen, excluded.last_seen)`,
			h.Host, h.Cores, h.LastSeen.UnixMilli()); err != nil {
			return apperr.Internal("upsert host", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return apperr.Internal("commit metrics insert", err)
	}
	if full {
		return ErrFull
	}
	return nil
}

func insertSampleChunk(ctx context.Context, tx *sql.Tx, samples []hostmetrics.Sample) error {
	var b strings.Builder
	b.WriteString(`INSERT INTO samples_1m (host, metric, ts, value) VALUES `)
	args := make([]any, 0, len(samples)*4)
	for i, s := range samples {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`(?,?,?,?)`)
		args = append(args, s.Host, s.Metric, s.TS.UnixMilli(), s.Value)
	}
	b.WriteString(` ON CONFLICT(host, metric, ts) DO UPDATE SET value = excluded.value`)
	_, err := tx.ExecContext(ctx, b.String(), args...)
	return err
}

// Host is one row of the hosts table.
type Host struct {
	Host     string    `json:"host"`
	Cores    int       `json:"cores"`
	LastSeen time.Time `json:"lastSeen"`
}

// Hosts lists every host that reported, most recently seen first.
func (d *DB) Hosts(ctx context.Context) ([]Host, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT host, cores, last_seen FROM hosts ORDER BY last_seen DESC`)
	if err != nil {
		return nil, apperr.Internal("list hosts", err)
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		var h Host
		var seen int64
		if err := rows.Scan(&h.Host, &h.Cores, &seen); err != nil {
			return nil, apperr.Internal("scan host", err)
		}
		h.LastSeen = time.UnixMilli(seen).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("iterate hosts", err)
	}
	return out, nil
}

// Point is one value of a series. Min and Max equal Value for minute data.
type Point struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
	Min   float64   `json:"min"`
	Max   float64   `json:"max"`
}

// Series returns one host metric between from and to. hourly selects the
// hourly rollups (avg as Value) instead of minute samples.
func (d *DB) Series(ctx context.Context, host, metric string, from, to time.Time, hourly bool) ([]Point, error) {
	q := `SELECT ts, value, value, value FROM samples_1m WHERE host = ? AND metric = ? AND ts >= ? AND ts <= ? ORDER BY ts`
	if hourly {
		q = `SELECT ts, avg, min, max FROM samples_1h WHERE host = ? AND metric = ? AND ts >= ? AND ts <= ? ORDER BY ts`
	}
	rows, err := d.read.QueryContext(ctx, q, host, metric, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, apperr.Internal("query series", err)
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var ts int64
		if err := rows.Scan(&ts, &p.Value, &p.Min, &p.Max); err != nil {
			return nil, apperr.Internal("scan point", err)
		}
		p.TS = time.UnixMilli(ts).UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("iterate series", err)
	}
	return out, nil
}

// Metrics lists the metric names a host reported since `since`.
func (d *DB) Metrics(ctx context.Context, host string, since time.Time) ([]string, error) {
	rows, err := d.read.QueryContext(ctx,
		`SELECT DISTINCT metric FROM samples_1m WHERE host = ? AND ts >= ? ORDER BY metric`, host, since.UnixMilli())
	if err != nil {
		return nil, apperr.Internal("list metrics", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, apperr.Internal("scan metric", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
