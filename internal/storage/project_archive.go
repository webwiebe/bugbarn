package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// LastActivity returns the newest time the project received an event, a log
// line or a page view, and false when it never received any. Events the server
// files into the project itself (monitor misses, detections, silence findings)
// do not count, so a silent project is not kept alive by its own alerts. Each
// read is one index seek.
func (s *ProjectStore) LastActivity(ctx context.Context, projectID int64) (time.Time, bool, error) {
	var last time.Time
	var found bool
	note := func(t time.Time, ok bool) {
		if ok && (!found || t.After(last)) {
			last, found = t, true
		}
	}

	t, ok, err := s.NewestEventAt(ctx, projectID)
	if err != nil {
		return time.Time{}, false, err
	}
	note(t, ok)

	if t, ok, err = s.NewestLogAt(ctx, projectID); err != nil {
		return time.Time{}, false, err
	}
	note(t, ok)

	var ts sql.NullInt64
	err = s.readDB().QueryRowContext(ctx,
		`SELECT ts FROM analytics_pageviews WHERE project_id = ? ORDER BY ts DESC LIMIT 1`, projectID).Scan(&ts)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, wrapErr(err, "project last activity")
	}
	if ts.Valid {
		note(time.Unix(ts.Int64, 0).UTC(), true)
	}
	return last, found, nil
}

// ArchiveProject sets the project's status to archived once it has been quiet
// for at least quietFor, and returns its last activity (zero when it never had
// any). A project that was active inside the window is a Conflict and stays as
// it is.
func (s *ProjectStore) ArchiveProject(ctx context.Context, slug string, quietFor time.Duration) (time.Time, error) {
	p, err := s.ProjectBySlug(ctx, slug)
	if err != nil {
		return time.Time{}, err
	}
	last, found, err := s.LastActivity(ctx, p.ID)
	if err != nil {
		return time.Time{}, err
	}
	if found && time.Since(last) < quietFor {
		return last, apperr.Conflict(fmt.Sprintf(
			"project %q received data at %s, inside the %s quiet period",
			slug, last.Format(time.RFC3339), quietFor), nil)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET status = ? WHERE id = ?`, domain.ProjectStatusArchived, p.ID); err != nil {
		return last, wrapErr(err, "archive project")
	}
	return last, nil
}

// UnarchiveProject returns an archived project to active.
func (s *ProjectStore) UnarchiveProject(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET status = 'active' WHERE slug = ? AND status = ?`, slug, domain.ProjectStatusArchived)
	if err != nil {
		return wrapErr(err, "unarchive project")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFound("no archived project with that slug", nil)
	}
	return nil
}
