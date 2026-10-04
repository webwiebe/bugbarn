package telemetrydb

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

// insertChunk rows per multi-row INSERT: 13 columns x 500 stays far below
// SQLite's 32766 bound-parameter limit and keeps one statement short.
const insertChunk = 500

const securityCols = 13

// InsertSecurity writes records in one transaction. It refuses with ErrFull
// while the file is at its cap; callers run detections before inserting, so a
// refused insert loses forensics only.
func (d *DB) InsertSecurity(ctx context.Context, recs []secnorm.Record, receivedAt time.Time) error {
	if len(recs) == 0 {
		return nil
	}
	if d.Full() {
		return ErrFull
	}
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return apperr.Internal("begin security insert", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for start := 0; start < len(recs); start += insertChunk {
		end := min(start+insertChunk, len(recs))
		if err := insertSecurityChunk(ctx, tx, recs[start:end], receivedAt.UnixMilli()); err != nil {
			return apperr.Internal("insert security logs", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return apperr.Internal("commit security insert", err)
	}
	return nil
}

func insertSecurityChunk(ctx context.Context, tx *sql.Tx, recs []secnorm.Record, receivedAt int64) error {
	var b strings.Builder
	b.WriteString(`INSERT INTO security_logs
		(ts, received_at, source, host, kind, src_ip, username, action, status, method, path, message, raw) VALUES `)
	args := make([]any, 0, len(recs)*securityCols)
	for i, r := range recs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		var user any
		if r.User != "" {
			user = r.User
		}
		args = append(args, r.TS.UnixMilli(), receivedAt, r.Source, r.Host, r.Kind, r.SrcIP, user,
			r.Action, r.Status, r.Method, r.Path, r.Message, r.Raw)
	}
	_, err := tx.ExecContext(ctx, b.String(), args...)
	return err
}

// SecurityQuery filters a security log search. From/To are required; Before
// is a keyset cursor (an id; 0 starts at the newest).
type SecurityQuery struct {
	From, To time.Time
	Source   string
	Host     string
	Kind     string
	SrcIP    string
	User     string
	Status   int
	Text     string
	Before   int64
	Limit    int
}

// SecurityRow is one stored security log line.
type SecurityRow struct {
	ID int64
	secnorm.Record
}

// SearchSecurity returns matching rows newest first. Equality filters on
// src_ip, host, user and source use their (col, ts) indexes; the text filter
// is a substring scan over the rows the other filters leave.
func (d *DB) SearchSecurity(ctx context.Context, q SecurityQuery) ([]SecurityRow, error) {
	where := []string{"ts >= ?", "ts <= ?"}
	args := []any{q.From.UnixMilli(), q.To.UnixMilli()}
	for _, f := range [][2]string{{"source", q.Source}, {"host", q.Host}, {"kind", q.Kind}, {"src_ip", q.SrcIP}, {"username", q.User}} {
		if f[1] != "" {
			where = append(where, f[0]+" = ?")
			args = append(args, f[1])
		}
	}
	if q.Status != 0 {
		where = append(where, "status = ?")
		args = append(args, q.Status)
	}
	if q.Text != "" {
		where = append(where, "(message LIKE ? ESCAPE '\\' OR path LIKE ? ESCAPE '\\')")
		pat := "%" + escapeLike(q.Text) + "%"
		args = append(args, pat, pat)
	}
	if q.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, q.Before)
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit)
	rows, err := d.read.QueryContext(ctx, `SELECT
		id, ts, source, host, kind, src_ip, COALESCE(username, ''), action, status, method, path, message, raw
		FROM security_logs WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, apperr.Internal("search security logs", err)
	}
	defer rows.Close()
	var out []SecurityRow
	for rows.Next() {
		var r SecurityRow
		var ts int64
		if err := rows.Scan(&r.ID, &ts, &r.Source, &r.Host, &r.Kind, &r.SrcIP, &r.User,
			&r.Action, &r.Status, &r.Method, &r.Path, &r.Message, &r.Raw); err != nil {
			return nil, apperr.Internal("scan security log", err)
		}
		r.TS = time.UnixMilli(ts).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal("iterate security logs", err)
	}
	return out, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Meta reads a key from the meta table; "" when unset.
func (d *DB) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := d.read.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", apperr.Internal("read meta", err)
	}
	return v, nil
}

// InsertSecurityWithMeta inserts records and sets a meta key in the same
// transaction, so a poller cursor never advances past rows that were lost.
// Unlike InsertSecurity it still advances the cursor when the file is full.
func (d *DB) InsertSecurityWithMeta(ctx context.Context, recs []secnorm.Record, receivedAt time.Time, key, value string) error {
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return apperr.Internal("begin security insert", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if !d.Full() {
		for start := 0; start < len(recs); start += insertChunk {
			end := min(start+insertChunk, len(recs))
			if err := insertSecurityChunk(ctx, tx, recs[start:end], receivedAt.UnixMilli()); err != nil {
				return apperr.Internal("insert security logs", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return apperr.Internal("write meta", err)
	}
	if err := tx.Commit(); err != nil {
		return apperr.Internal("commit security insert", err)
	}
	return nil
}
