package storage

import (
	"context"
	"time"
)

// ProjectActivity counts, per active project, the issues last seen recently and
// earlier in the lookback window.
type ProjectActivity struct {
	ProjectID int64
	Slug      string
	// Recent counts issues last seen within silentFor of now.
	Recent int
	// Earlier counts issues last seen between lookback+silentFor and silentFor ago.
	Earlier int
}

// ProjectActivity reports issue activity per project so a caller can tell a
// project that went quiet from one that never reported. Only projects with an
// issue last seen inside silentFor+lookback are listed. It reads the issues
// table, never events, so it stays cheap on a large database.
func (s *ProjectStore) ProjectActivity(
	ctx context.Context, now time.Time, silentFor, lookback time.Duration,
) ([]ProjectActivity, error) {
	recentFrom := now.Add(-silentFor).UTC().Format(time.RFC3339)
	earlierFrom := now.Add(-silentFor - lookback).UTC().Format(time.RFC3339)

	rows, err := s.readDB().QueryContext(ctx, `
		SELECT p.id, p.slug,
			SUM(CASE WHEN i.last_seen >= ? THEN 1 ELSE 0 END),
			SUM(CASE WHEN i.last_seen < ? THEN 1 ELSE 0 END)
		FROM projects p
		JOIN issues i ON i.project_id = p.id
		WHERE p.status = 'active' AND i.last_seen >= ?
		GROUP BY p.id, p.slug`, recentFrom, recentFrom, earlierFrom)
	if err != nil {
		return nil, wrapErr(err, "project activity")
	}
	defer rows.Close()

	var out []ProjectActivity
	for rows.Next() {
		var a ProjectActivity
		if err := rows.Scan(&a.ProjectID, &a.Slug, &a.Recent, &a.Earlier); err != nil {
			return nil, wrapErr(err, "scan project activity")
		}
		out = append(out, a)
	}
	return out, wrapErr(rows.Err(), "project activity")
}
