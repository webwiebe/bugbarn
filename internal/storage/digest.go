package storage

import (
	"context"
	"database/sql"
	"time"
)

// WeeklyDigest returns aggregate error stats for the given project since the
// given time. All queries run under the provided context deadline.
func (s *DigestStore) WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (DigestData, error) {
	sinceStr := since.UTC().Format(time.RFC3339)

	var d DigestData

	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE project_id = ? AND received_at >= ?`,
		projectID, sinceStr,
	).Scan(&d.TotalEvents); err != nil && err != sql.ErrNoRows {
		return d, err
	}

	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM issues WHERE project_id = ? AND first_seen >= ?`,
		projectID, sinceStr,
	).Scan(&d.NewIssues); err != nil && err != sql.ErrNoRows {
		return d, err
	}

	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM issues WHERE project_id = ? AND status = 'resolved' AND resolved_at >= ?`,
		projectID, sinceStr,
	).Scan(&d.ResolvedIssues); err != nil && err != sql.ErrNoRows {
		return d, err
	}

	if err := s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM regression_events WHERE project_id = ? AND regressed_at >= ?`,
		projectID, sinceStr,
	).Scan(&d.Regressions); err != nil && err != sql.ErrNoRows {
		return d, err
	}

	rows, err := s.readDB().QueryContext(ctx, `
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
