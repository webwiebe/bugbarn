package storage

import (
	"context"
	"database/sql"
	"time"
)

// DigestIssue is a summary of a single issue for the weekly digest.
type DigestIssue struct {
	ID         string
	Title      string
	EventCount int
	Status     string
}

// DigestData holds aggregate stats for the weekly digest.
type DigestData struct {
	TotalEvents    int
	NewIssues      int
	ResolvedIssues int
	Regressions    int
	TopIssues      []DigestIssue
}

// WeeklyDigest returns aggregate error stats for the given project since the
// given time. All queries run under the provided context deadline.
func (s *Store) WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (DigestData, error) {
	sinceStr := since.UTC().Format(time.RFC3339)

	var d DigestData

	row := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*)                                                                          AS total_events,
			COUNT(*) FILTER (WHERE i.first_seen >= ?)                                         AS new_issues,
			COUNT(*) FILTER (WHERE i.status = 'resolved' AND i.resolved_at >= ?)              AS resolved_issues,
			COUNT(*) FILTER (WHERE i.last_regressed_at IS NOT NULL AND i.last_regressed_at >= ?) AS regressions
		FROM issues i
		JOIN events e ON e.issue_id = i.id AND e.project_id = ?
		WHERE e.project_id = ?
		  AND e.received_at >= ?
	`, sinceStr, sinceStr, sinceStr, projectID, projectID, sinceStr)

	if err := row.Scan(&d.TotalEvents, &d.NewIssues, &d.ResolvedIssues, &d.Regressions); err != nil && err != sql.ErrNoRows {
		return d, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.title, COUNT(e.id) AS event_count, i.status
		FROM events e
		JOIN issues i ON i.id = e.issue_id
		WHERE e.project_id = ?
		  AND e.received_at >= ?
		GROUP BY i.id
		ORDER BY event_count DESC
		LIMIT 5
	`, projectID, sinceStr)
	if err != nil {
		return d, err
	}
	defer rows.Close()

	for rows.Next() {
		var iss DigestIssue
		if err := rows.Scan(&iss.ID, &iss.Title, &iss.EventCount, &iss.Status); err != nil {
			return d, err
		}
		d.TopIssues = append(d.TopIssues, iss)
	}
	return d, rows.Err()
}
