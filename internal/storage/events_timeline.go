package storage

import (
	"context"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// ErrorSeverities are the stored severity labels that count as an error event
// on the timeline. normalize upper-cases every known level, so these match the
// stored text exactly.
var ErrorSeverities = []string{"ERROR", "CRITICAL", "FATAL", "ALERT", "EMERGENCY"}

// TimelineRow is the error-event weight of one project in one UTC hour.
type TimelineRow struct {
	ProjectID   int64
	ProjectSlug string
	ProjectName string
	Hour        time.Time
	Count       int64
}

// ErrorTimelineRows returns, per project and UTC hour, the sample-weighted
// number of error events received at or after since. The request scope applies:
// a project in the context (API key or X-BugBarn-Project) narrows the result to
// it, a group narrows it to the members, and no project means all projects.
//
// The query is answered from idx_events_error_timeline alone (migration 00021).
// INDEXED BY pins it: without table statistics the planner prefers
// idx_events_project_sample_weight to avoid the GROUP BY sort, which cannot
// serve the range or the severity filter and reads the table row by row.
// Hours are the finest grain; the service folds them into wider buckets, which
// keeps the SQL grouping on a cheap substr instead of a date parse per row.
func (s *EventStore) ErrorTimelineRows(ctx context.Context, since time.Time) ([]TimelineRow, error) {
	scope, scopeArgs := timelineScope(ctx)
	args := []any{since.UTC().Format(time.RFC3339Nano)}
	marks := make([]string, len(ErrorSeverities))
	for i, sev := range ErrorSeverities {
		marks[i] = "?"
		args = append(args, sev)
	}
	args = append(args, scopeArgs...)

	rows, err := s.readDB().QueryContext(ctx, `
SELECT p.id, p.slug, p.name, t.hour, t.cnt
FROM (
	SELECT project_id, substr(received_at, 1, 13) AS hour, SUM(sample_weight) AS cnt
	FROM events INDEXED BY idx_events_error_timeline
	WHERE received_at >= ? AND severity IN (`+strings.Join(marks, ",")+`)`+scope+`
	GROUP BY project_id, hour
) t
JOIN projects p ON p.id = t.project_id
ORDER BY t.hour`, args...)
	if err != nil {
		return nil, wrapErr(err, "error timeline")
	}
	defer rows.Close()

	var out []TimelineRow
	for rows.Next() {
		var r TimelineRow
		var hour string
		if err := rows.Scan(&r.ProjectID, &r.ProjectSlug, &r.ProjectName, &hour, &r.Count); err != nil {
			return nil, wrapErr(err, "scan error timeline")
		}
		parsed, err := time.Parse("2006-01-02T15", hour)
		if err != nil {
			continue
		}
		r.Hour = parsed.UTC()
		out = append(out, r)
	}
	return out, wrapErr(rows.Err(), "error timeline rows")
}

// timelineScope returns the extra WHERE clause and its arguments for the
// request's project or group scope.
func timelineScope(ctx context.Context) (string, []any) {
	if id, ok := domain.ProjectIDFromContext(ctx); ok && id != 0 {
		return " AND project_id = ?", []any{id}
	}
	ids, ok := domain.ProjectIDsFromContext(ctx)
	if !ok {
		return "", nil
	}
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	return " AND project_id IN (" + strings.Join(marks, ",") + ")", args
}
