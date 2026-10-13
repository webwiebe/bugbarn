package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// ListEnabledMonitors returns every enabled monitor of an active project with
// its project slug. The miss detector scans this list; it stays small because a
// project holds at most MaxMonitorsPerProject.
func (s *MonitorStore) ListEnabledMonitors(ctx context.Context) ([]domain.Monitor, error) {
	rows, err := s.readDB().QueryContext(ctx, `
SELECT m.id, m.project_id, m.slug, m.interval_s, m.grace_s, m.enabled, m.created_at,
  m.last_checkin_at, m.last_status, m.state, m.state_since, m.last_emitted_at, p.slug
FROM monitors m JOIN projects p ON p.id = m.project_id
WHERE m.enabled = 1 AND p.status = 'active'
ORDER BY m.id`)
	if err != nil {
		return nil, wrapErr(err, "list enabled monitors")
	}
	defer rows.Close()
	var out []domain.Monitor
	for rows.Next() {
		var projectSlug string
		m, err := scanMonitor(withExtra{rows, &projectSlug})
		if err != nil {
			return nil, wrapErr(err, "scan monitor")
		}
		m.ProjectSlug = projectSlug
		out = append(out, m)
	}
	return out, wrapErr(rows.Err(), "list enabled monitors")
}

// withExtra scans one trailing column after the monitor columns.
type withExtra struct {
	rows  rowScanner
	extra *string
}

func (w withExtra) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.extra)...) }

// MarkMonitorMissed moves an ok monitor to missed. It only applies while the
// monitor still carries the last check-in the detector saw, so a check-in that
// lands between the scan and this update wins. It reports whether it applied.
func (s *MonitorStore) MarkMonitorMissed(ctx context.Context, id int64, seenCheckIn, now time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, errMonitorReadOnly
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE monitors SET state = ?, state_since = ?
WHERE id = ? AND state = ? AND last_checkin_at = ?`,
		domain.MonitorStateMissed, formatTime(now), id, domain.MonitorStateOK, formatTimeOrEmpty(seenCheckIn))
	if err != nil {
		return false, wrapErr(err, "mark monitor missed")
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkMonitorEmitted records that the incident of a monitor was reported.
func (s *MonitorStore) MarkMonitorEmitted(ctx context.Context, id int64, now time.Time) error {
	if s == nil || s.db == nil {
		return errMonitorReadOnly
	}
	_, err := s.db.ExecContext(ctx, `UPDATE monitors SET last_emitted_at = ? WHERE id = ?`, formatTime(now), id)
	return wrapErr(err, "mark monitor emitted")
}

// DeleteMonitor removes a monitor of a project by slug. Its issues stay: they
// belong to the project, and someone may still be working on them. A job that
// checks in again with an interval creates the monitor afresh.
func (s *MonitorStore) DeleteMonitor(ctx context.Context, projectID int64, slug string) error {
	if s == nil || s.db == nil {
		return errMonitorReadOnly
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE project_id = ? AND slug = ?`, projectID, slug)
	if err != nil {
		return wrapErr(err, "delete monitor")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return wrapNotFound(sql.ErrNoRows, "monitor not found")
	}
	return nil
}
