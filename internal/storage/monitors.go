package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// MaxMonitorsPerProject bounds how many monitors one project can hold, so a
// check-in carrying a fresh slug each time cannot grow the table without limit.
const MaxMonitorsPerProject = 100

var errMonitorReadOnly = apperr.Internal("storage is read-only", errors.New("no write connection"))

// MonitorCheckIn is one check-in as the storage layer applies it. Interval and
// Grace are nil when the caller did not send them: the stored values stay, and
// an unknown monitor is then not created.
type MonitorCheckIn struct {
	ProjectID int64
	Slug      string
	Status    string
	Interval  *time.Duration
	Grace     *time.Duration
	At        time.Time
}

// MonitorCheckInResult is the monitor after the check-in plus what it was
// before, so a caller can tell a recovery from a plain check-in.
type MonitorCheckInResult struct {
	Monitor       domain.Monitor
	Created       bool
	PreviousState string
}

const monitorColumns = `id, project_id, slug, interval_s, grace_s, enabled, created_at,
  last_checkin_at, last_status, state, state_since, last_emitted_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanMonitor(row rowScanner) (domain.Monitor, error) {
	var m domain.Monitor
	var interval, grace, enabled int64
	var created, lastCheckIn, since, emitted string
	if err := row.Scan(&m.ID, &m.ProjectID, &m.Slug, &interval, &grace, &enabled, &created,
		&lastCheckIn, &m.LastStatus, &m.State, &since, &emitted); err != nil {
		return domain.Monitor{}, err
	}
	m.Interval = time.Duration(interval) * time.Second
	m.Grace = time.Duration(grace) * time.Second
	m.Enabled = enabled != 0
	m.CreatedAt, _ = parseTime(created)
	m.LastCheckInAt = parseTimeOrZero(lastCheckIn)
	m.StateSince = parseTimeOrZero(since)
	m.LastEmittedAt = parseTimeOrZero(emitted)
	return m, nil
}

func parseTimeOrZero(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := parseTime(value)
	return t
}

// RecordMonitorCheckIn applies a check-in in one transaction: it creates the
// monitor when the check-in carries an interval, updates the configuration
// that was sent, and stores the latest status and the state it leads to. An
// "ok" check-in returns the state to ok, an "error" one sets it to error.
func (s *MonitorStore) RecordMonitorCheckIn(ctx context.Context, in MonitorCheckIn) (MonitorCheckInResult, error) {
	if s == nil || s.db == nil {
		return MonitorCheckInResult{}, errMonitorReadOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MonitorCheckInResult{}, wrapErr(err, "begin monitor check-in")
	}
	defer func() { _ = tx.Rollback() }()

	res, err := applyMonitorCheckIn(ctx, tx, in)
	if err != nil {
		return MonitorCheckInResult{}, err
	}
	return res, wrapErr(tx.Commit(), "commit monitor check-in")
}

func applyMonitorCheckIn(ctx context.Context, tx *sql.Tx, in MonitorCheckIn) (MonitorCheckInResult, error) {
	m, err := scanMonitor(tx.QueryRowContext(ctx,
		`SELECT `+monitorColumns+` FROM monitors WHERE project_id = ? AND slug = ?`, in.ProjectID, in.Slug))
	if errors.Is(err, sql.ErrNoRows) {
		return createMonitorFromCheckIn(ctx, tx, in)
	}
	if err != nil {
		return MonitorCheckInResult{}, wrapErr(err, "load monitor")
	}

	res := MonitorCheckInResult{PreviousState: m.State}
	if in.Interval != nil {
		m.Interval = *in.Interval
	}
	if in.Grace != nil {
		m.Grace = *in.Grace
	}
	m.LastCheckInAt = in.At
	m.LastStatus = in.Status
	m.State = domain.MonitorStateOK
	if in.Status == domain.CheckInError {
		m.State = domain.MonitorStateError
	}
	if m.State != res.PreviousState {
		m.StateSince = in.At
	}
	_, err = tx.ExecContext(ctx, `
UPDATE monitors SET interval_s = ?, grace_s = ?, last_checkin_at = ?, last_status = ?, state = ?, state_since = ?
WHERE id = ?`,
		int64(m.Interval/time.Second), int64(m.Grace/time.Second), formatTime(m.LastCheckInAt),
		m.LastStatus, m.State, formatTimeOrEmpty(m.StateSince), m.ID)
	res.Monitor = m
	return res, wrapErr(err, "update monitor")
}

func createMonitorFromCheckIn(ctx context.Context, tx *sql.Tx, in MonitorCheckIn) (MonitorCheckInResult, error) {
	if in.Interval == nil {
		return MonitorCheckInResult{}, apperr.NotFound("monitor not found: send an interval to create it", nil)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE project_id = ?`, in.ProjectID).Scan(&n); err != nil {
		return MonitorCheckInResult{}, wrapErr(err, "count monitors")
	}
	if n >= MaxMonitorsPerProject {
		return MonitorCheckInResult{}, apperr.Conflict("monitor limit reached for this project", nil)
	}
	m := domain.Monitor{
		ProjectID: in.ProjectID, Slug: in.Slug, Interval: *in.Interval, Enabled: true,
		CreatedAt: in.At, LastCheckInAt: in.At, LastStatus: in.Status, State: domain.MonitorStateOK,
	}
	if in.Grace != nil {
		m.Grace = *in.Grace
	}
	if in.Status == domain.CheckInError {
		m.State = domain.MonitorStateError
		m.StateSince = in.At
	}
	r, err := tx.ExecContext(ctx, `
INSERT INTO monitors (project_id, slug, interval_s, grace_s, enabled, created_at, last_checkin_at, last_status, state, state_since)
VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		m.ProjectID, m.Slug, int64(m.Interval/time.Second), int64(m.Grace/time.Second),
		formatTime(m.CreatedAt), formatTime(m.LastCheckInAt), m.LastStatus, m.State, formatTimeOrEmpty(m.StateSince))
	if err != nil {
		return MonitorCheckInResult{}, wrapErr(err, "create monitor")
	}
	m.ID, _ = r.LastInsertId()
	return MonitorCheckInResult{Monitor: m, Created: true, PreviousState: domain.MonitorStateOK}, nil
}

// GetMonitor loads one monitor of a project by slug.
func (s *MonitorStore) GetMonitor(ctx context.Context, projectID int64, slug string) (domain.Monitor, error) {
	m, err := scanMonitor(s.readDB().QueryRowContext(ctx,
		`SELECT `+monitorColumns+` FROM monitors WHERE project_id = ? AND slug = ?`, projectID, slug))
	if err != nil {
		return domain.Monitor{}, wrapErr(err, "get monitor")
	}
	return m, nil
}

// ListMonitors returns the monitors of a project ordered by slug.
func (s *MonitorStore) ListMonitors(ctx context.Context, projectID int64) ([]domain.Monitor, error) {
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT `+monitorColumns+` FROM monitors WHERE project_id = ? ORDER BY slug`, projectID)
	if err != nil {
		return nil, wrapErr(err, "list monitors")
	}
	defer rows.Close()
	var out []domain.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, wrapErr(err, "scan monitor")
		}
		out = append(out, m)
	}
	return out, wrapErr(rows.Err(), "list monitors")
}
