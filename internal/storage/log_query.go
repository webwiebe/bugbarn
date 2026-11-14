package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// logScanWindow bounds how far back, in log row ids, a filtered read looks.
// Ids rise with insertion across all projects, so the window is a cheap proxy
// for "recent" that needs no index on received_at. Each project keeps at most
// logRetentionCap rows, so the window covers the retained logs of a deployment
// with a few dozen busy projects; past that the answer reports itself truncated.
const logScanWindow = 200000

// logAttrMax bounds how many attribute filters one query may carry, and
// logAttrKeyMax how long an attribute key may be.
const (
	logAttrMax    = 8
	logAttrKeyMax = 128
)

func invalidLogQuery(msg string) error { return apperr.InvalidInput(msg, nil) }

// logPath renders a top-level JSON path for a key, quoted so dots and spaces in
// the key stay part of the name.
func logPath(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `"`, `\"`)
	return `$."` + key + `"`
}

func validLogAttrs(attrs []domain.LogAttrFilter) error {
	if len(attrs) > logAttrMax {
		return invalidLogQuery(fmt.Sprintf("at most %d attribute filters", logAttrMax))
	}
	for _, a := range attrs {
		if a.Key == "" || len(a.Key) > logAttrKeyMax {
			return invalidLogQuery("attribute key must be 1 to 128 bytes")
		}
	}
	return nil
}

// logQueryWhere builds the WHERE clause for a LogQuery. minID > 0 adds the scan
// window as a lower id bound.
func logQueryWhere(q domain.LogQuery, minID int64) (string, []any) {
	var args []any
	var conditions []string

	if q.ProjectID != 0 {
		conditions = append(conditions, "le.project_id = ?")
		args = append(args, q.ProjectID)
	}
	if minID > 0 {
		conditions = append(conditions, "le.id > ?")
		args = append(args, minID)
	}
	if q.LevelMin > 0 {
		conditions = append(conditions, "le.level_num >= ?")
		args = append(args, q.LevelMin)
	}
	if q.Q != "" {
		conditions = append(conditions, "le.message LIKE ?")
		args = append(args, "%"+q.Q+"%")
	}
	if !q.From.IsZero() {
		conditions = append(conditions, "le.received_at >= ?")
		args = append(args, formatTime(q.From))
	}
	if !q.To.IsZero() {
		conditions = append(conditions, "le.received_at < ?")
		args = append(args, formatTime(q.To))
	}
	for _, a := range q.Attrs {
		conditions = append(conditions, "CAST(json_extract(le.data_json, ?) AS TEXT) = ?")
		args = append(args, logPath(a.Key), a.Value)
	}
	if q.BeforeID > 0 {
		conditions = append(conditions, "le.id < ?")
		args = append(args, q.BeforeID)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

// scanWindowMinID returns the lowest id (exclusive) a filtered read looks at.
// A plain, unfiltered list keeps its old shape and reads no window.
func (s *LogStore) scanWindowMinID(ctx context.Context, q domain.LogQuery) (int64, error) {
	if q.From.IsZero() && len(q.Attrs) == 0 {
		return 0, nil
	}
	return s.windowFloor(ctx)
}

func (s *LogStore) windowFloor(ctx context.Context) (int64, error) {
	var maxID int64
	if err := s.readDB().QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM log_entries`).Scan(&maxID); err != nil {
		return 0, wrapErr(err, "log scan window")
	}
	if maxID <= logScanWindow {
		return 0, nil
	}
	return maxID - logScanWindow, nil
}

const logListSelect = `
		SELECT le.id, le.project_id, le.received_at, le.level_num, le.level, le.message, le.data_json, COALESCE(p.slug, '') AS project_slug
		FROM log_entries le
		LEFT JOIN projects p ON p.id = le.project_id
		`

// QueryLogEntries returns up to limit entries matching q, newest first.
func (s *LogStore) QueryLogEntries(ctx context.Context, q domain.LogQuery, limit int) ([]domain.LogEntry, error) {
	if err := validLogAttrs(q.Attrs); err != nil {
		return nil, wrapErr(err, "QueryLogEntries")
	}
	minID, err := s.scanWindowMinID(ctx, q)
	if err != nil {
		return nil, wrapErr(err, "QueryLogEntries")
	}
	where, args := logQueryWhere(q, minID)
	args = append(args, limit)

	rows, err := s.readDB().QueryContext(ctx, logListSelect+where+` ORDER BY le.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, wrapErr(err, "list log entries")
	}
	defer rows.Close()

	var entries []domain.LogEntry
	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
			return nil, wrapErr(err, "QueryLogEntries")
		}
		entries = append(entries, e)
	}
	return entries, wrapErr(rows.Err(), "list log entries")
}

// LogHistogram counts entries per bucket and level over [q.From, q.To). Both
// bounds are required. Empty buckets are present so the chart has no holes.
func (s *LogStore) LogHistogram(ctx context.Context, q domain.LogQuery, bucket time.Duration) (domain.LogHistogram, error) {
	if err := validLogAttrs(q.Attrs); err != nil {
		return domain.LogHistogram{}, wrapErr(err, "LogHistogram")
	}
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) || bucket < time.Second {
		return domain.LogHistogram{}, invalidLogQuery("histogram needs from < to and a bucket of at least one second")
	}
	q.BeforeID = 0
	minID, err := s.windowFloor(ctx)
	if err != nil {
		return domain.LogHistogram{}, wrapErr(err, "LogHistogram")
	}
	where, args := logQueryWhere(q, minID)
	secs := int64(bucket / time.Second)
	fromEpoch := q.From.UTC().Unix()
	args = append([]any{fromEpoch, secs}, args...)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT (CAST(strftime('%s', substr(le.received_at, 1, 19)) AS INTEGER) - ?) / ? AS b, le.level, COUNT(*)
		FROM log_entries le `+where+` GROUP BY b, le.level`, args...)
	if err != nil {
		return domain.LogHistogram{}, wrapErr(err, "log histogram")
	}
	defer rows.Close()

	n := int((q.To.Sub(q.From) + bucket - 1) / bucket)
	out := domain.LogHistogram{
		From: q.From.UTC(), To: q.To.UTC(), BucketSeconds: int(secs), Buckets: make([]domain.LogHistogramBucket, n),
	}
	for i := range out.Buckets {
		out.Buckets[i] = domain.LogHistogramBucket{Start: q.From.UTC().Add(time.Duration(i) * bucket), Counts: map[string]int{}}
	}
	if err := fillHistogramBuckets(rows, out.Buckets); err != nil {
		return domain.LogHistogram{}, wrapErr(err, "LogHistogram")
	}
	out.Truncated, err = s.windowTruncated(ctx, minID, q.From)
	return out, wrapErr(err, "LogHistogram")
}

// fillHistogramBuckets adds the (bucket index, level, count) rows to buckets.
func fillHistogramBuckets(rows *sql.Rows, buckets []domain.LogHistogramBucket) error {
	for rows.Next() {
		var idx int64
		var level string
		var count int
		if err := rows.Scan(&idx, &level, &count); err != nil {
			return wrapErr(err, "log histogram")
		}
		if idx >= 0 && int(idx) < len(buckets) {
			buckets[idx].Counts[level] += count
		}
	}
	return wrapErr(rows.Err(), "log histogram")
}

// windowTruncated reports whether the scan window starts after from, so rows
// inside the range may sit below it.
func (s *LogStore) windowTruncated(ctx context.Context, minID int64, from time.Time) (bool, error) {
	if minID == 0 {
		return false, nil
	}
	var oldest string
	err := s.readDB().QueryRowContext(ctx,
		`SELECT received_at FROM log_entries WHERE id > ? ORDER BY id LIMIT 1`, minID).Scan(&oldest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrapErr(err, "log scan window")
	}
	return from.IsZero() || oldest > formatTime(from), nil
}

const (
	logFacetKeys   = 50
	logFacetValues = 10
	logFacetValLen = 200
)

// LogAttributeKeys lists the top-level attribute keys seen in the entries
// matching q, most common first.
func (s *LogStore) LogAttributeKeys(ctx context.Context, q domain.LogQuery) (domain.LogFacets, error) {
	return s.logFacets(ctx, q, "", facetSQL{
		head:     `SELECT j.key, COUNT(*) AS n FROM log_entries le, json_each(le.data_json) j `,
		tail:     ` GROUP BY j.key ORDER BY n DESC, j.key LIMIT ?`,
		tailArgs: []any{logFacetKeys},
	})
}

// LogAttributeValues lists the top values of one attribute key in the entries
// matching q, most common first.
func (s *LogStore) LogAttributeValues(ctx context.Context, q domain.LogQuery, key string) (domain.LogFacets, error) {
	if key == "" || len(key) > logAttrKeyMax {
		return domain.LogFacets{}, invalidLogQuery("attribute key must be 1 to 128 bytes")
	}
	path := logPath(key)
	return s.logFacets(ctx, q, key, facetSQL{
		head: `SELECT v, COUNT(*) AS n FROM (
			SELECT substr(CAST(json_extract(le.data_json, ?) AS TEXT), 1, ?) AS v FROM log_entries le `,
		headArgs: []any{path, logFacetValLen},
		tail:     ` AND json_extract(le.data_json, ?) IS NOT NULL) GROUP BY v ORDER BY n DESC, v LIMIT ?`,
		tailArgs: []any{path, logFacetValues},
	})
}

// facetSQL is a facet query around the shared WHERE clause. The clause always
// exists (the scan window or "1 = 1"), so a tail can continue it with AND.
type facetSQL struct {
	head, tail         string
	headArgs, tailArgs []any
}

func (s *LogStore) logFacets(ctx context.Context, q domain.LogQuery, key string, f facetSQL) (domain.LogFacets, error) {
	if err := validLogAttrs(q.Attrs); err != nil {
		return domain.LogFacets{}, wrapErr(err, "logFacets")
	}
	q.BeforeID = 0
	minID, err := s.windowFloor(ctx)
	if err != nil {
		return domain.LogFacets{}, wrapErr(err, "logFacets")
	}
	where, whereArgs := logQueryWhere(q, minID)
	if where == "" {
		where = "WHERE 1 = 1"
	}
	args := append(append(append([]any{}, f.headArgs...), whereArgs...), f.tailArgs...)

	rows, err := s.readDB().QueryContext(ctx, f.head+where+f.tail, args...)
	if err != nil {
		return domain.LogFacets{}, wrapErr(err, "log facets")
	}
	defer rows.Close()

	out := domain.LogFacets{Key: key, Items: []domain.LogFacetCount{}}
	for rows.Next() {
		var item domain.LogFacetCount
		if err := rows.Scan(&item.Value, &item.Count); err != nil {
			return domain.LogFacets{}, wrapErr(err, "log facets")
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.LogFacets{}, wrapErr(err, "log facets")
	}
	out.Truncated, err = s.windowTruncated(ctx, minID, q.From)
	return out, wrapErr(err, "logFacets")
}
