package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// The silence reads, kept as constants so the plan test runs the same text.
const (
	newestLogSQL   = `SELECT received_at FROM log_entries WHERE project_id = ? ORDER BY id DESC LIMIT 1`
	recentLogsSQL  = `SELECT received_at FROM log_entries WHERE project_id = ? ORDER BY id DESC LIMIT ?`
	newestEventSQL = `SELECT received_at FROM events INDEXED BY idx_events_project_received_issue
		WHERE project_id = ? AND issue_id NOT IN (` + selfFiledIssuesSQL + `)
		ORDER BY received_at DESC, id DESC LIMIT 1`
	eventTimesSQL = `SELECT received_at FROM events INDEXED BY idx_events_project_received_issue
		WHERE project_id = ? AND received_at >= ? AND issue_id NOT IN (` + selfFiledIssuesSQL + `)
		ORDER BY received_at DESC, id DESC LIMIT ?`
	// selfFiledIssuesSQL selects the issues BugBarn files into a project itself
	// (monitor misses, detection hits, silence findings). They are not activity
	// of the project's app, so no silence rule counts their events. The range
	// tests on fingerprint use the UNIQUE(project_id, fingerprint) index.
	selfFiledIssuesSQL = `SELECT id FROM issues WHERE project_id = ? AND (
		(fingerprint >= 'monitor:' AND fingerprint < 'monitor;') OR
		(fingerprint >= 'detect:' AND fingerprint < 'detect;') OR
		(fingerprint >= 'silence:' AND fingerprint < 'silence;'))`
)

// SilenceWatchOff is the silence_watch value that opts a project out of the
// silence rules. Empty means watched.
const SilenceWatchOff = "off"

// SilenceProject names an active project the silence rules judge.
type SilenceProject struct {
	ID   int64
	Slug string
}

// ActiveSilenceProjects lists the active projects.
func (s *ProjectStore) ActiveSilenceProjects(ctx context.Context) ([]SilenceProject, error) {
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT id, slug FROM projects WHERE status = 'active' AND silence_watch != 'off' ORDER BY id`)
	if err != nil {
		return nil, wrapErr(err, "list silence projects")
	}
	defer rows.Close()
	var out []SilenceProject
	for rows.Next() {
		var p SilenceProject
		if err := rows.Scan(&p.ID, &p.Slug); err != nil {
			return nil, wrapErr(err, "scan silence project")
		}
		out = append(out, p)
	}
	return out, wrapErr(rows.Err(), "list silence projects")
}

// NewestLogAt returns when the project's newest retained log row arrived. The
// bool is false when the project holds no log rows. One index lookup through
// idx_log_entries_project_id.
func (s *ProjectStore) NewestLogAt(ctx context.Context, projectID int64) (time.Time, bool, error) {
	return s.newestTime(ctx, "newest log row", newestLogSQL, projectID)
}

// RecentLogTimes returns the arrival times of up to limit retained log rows,
// newest first. The log cap (logRetentionCap) bounds how far back it can see.
func (s *ProjectStore) RecentLogTimes(ctx context.Context, projectID int64, limit int) ([]time.Time, error) {
	return s.timeList(ctx, "recent log times", recentLogsSQL, projectID, limit)
}

// NewestEventAt returns when the project's newest stored event arrived. One
// index lookup through idx_events_project_received_at.
func (s *ProjectStore) NewestEventAt(ctx context.Context, projectID int64) (time.Time, bool, error) {
	return s.newestTime(ctx, "newest event", newestEventSQL, projectID, projectID)
}

// EventTimesSince returns the arrival times of up to limit stored events at or
// after since, newest first. Answered from idx_events_project_received_at
// alone. Adaptive sampling thins busy issues, so gaps read from here can run
// long; the event rule's floors account for that.
func (s *ProjectStore) EventTimesSince(ctx context.Context, projectID int64, since time.Time, limit int) ([]time.Time, error) {
	return s.timeList(ctx, "event times", eventTimesSQL, projectID, formatTime(since), projectID, limit)
}

// NewestKeyUse returns the newest last_used_at across the project's keys that
// can ingest (every scope but read). The bool is false when no such key has
// been used. Evidence only: last_used_at keeps no history.
func (s *ProjectStore) NewestKeyUse(ctx context.Context, projectID int64) (time.Time, bool, error) {
	return s.newestTime(ctx, "newest key use", `
		SELECT last_used_at FROM api_keys
		WHERE project_id = ? AND scope != 'read' AND last_used_at IS NOT NULL
		ORDER BY last_used_at DESC LIMIT 1`, projectID)
}

func (s *ProjectStore) newestTime(ctx context.Context, what, query string, args ...any) (time.Time, bool, error) {
	var raw string
	err := s.readDB().QueryRowContext(ctx, query, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, wrapErr(err, what)
	}
	at, err := parseTime(raw)
	if err != nil {
		return time.Time{}, false, wrapErr(err, what)
	}
	return at, true, nil
}

func (s *ProjectStore) timeList(ctx context.Context, what, query string, args ...any) ([]time.Time, error) {
	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapErr(err, what)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, wrapErr(err, what)
		}
		at, err := parseTime(raw)
		if err != nil {
			return nil, wrapErr(err, what)
		}
		out = append(out, at)
	}
	return out, wrapErr(rows.Err(), what)
}

// UpdateProjectSilenceWatch sets whether the silence rules judge a project. The
// value is empty (watched) or SilenceWatchOff.
func (s *ProjectStore) UpdateProjectSilenceWatch(ctx context.Context, slug, value string) error {
	if value != "" && value != SilenceWatchOff {
		return apperr.InvalidInput("silence_watch must be empty or \"off\"", nil)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET silence_watch = ? WHERE slug = ?`, value, slug)
	if err != nil {
		return wrapErr(err, "update project silence watch")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFound("project not found", nil)
	}
	return nil
}

// ProjectSilenceWatch returns a project's silence_watch value.
func (s *ProjectStore) ProjectSilenceWatch(ctx context.Context, slug string) (string, error) {
	var v string
	err := s.readDB().QueryRowContext(ctx, `SELECT silence_watch FROM projects WHERE slug = ?`, slug).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperr.NotFound("project not found", nil)
	}
	return v, wrapErr(err, "read project silence watch")
}
