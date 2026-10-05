package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// The methods below back the alert evaluator (internal/alert). They take an
// explicit project ID instead of reading it from the context, because the
// evaluator runs from the ingest pipeline rather than from a scoped request.

// ListAlertsForProject returns every alert rule of one project, newest first.
func (s *AlertStore) ListAlertsForProject(ctx context.Context, projectID int64) ([]Alert, error) {
	if projectID <= 0 {
		return nil, nil
	}
	alerts, err := s.ListAlerts(domain.WithProjectID(ctx, projectID))
	return alerts, wrapErr(err, "list alerts for project")
}

// RecordAlertFiring stores one firing of an alert for an issue.
//
// alert_firings.alert_id and issue_id are INTEGER columns that have always held
// the display IDs ("alert-000001", "issue-000001") as text. LastAlertFiring
// looks them up the same way, so the cooldown keeps working across the rows
// written before this method moved here.
func (s *AlertStore) RecordAlertFiring(ctx context.Context, alertID, issueID string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO alert_firings (alert_id, issue_id, fired_at)
VALUES (?, ?, CURRENT_TIMESTAMP)`,
		alertID,
		issueID,
	)
	return wrapErr(err, "record alert firing")
}

// LastAlertFiring returns when an alert last fired for an issue, or the zero
// time when it never did.
func (s *AlertStore) LastAlertFiring(ctx context.Context, alertID, issueID string) (time.Time, error) {
	var firedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT fired_at
FROM alert_firings
WHERE alert_id = ? AND issue_id = ?
ORDER BY fired_at DESC
LIMIT 1`,
		alertID,
		issueID,
	).Scan(&firedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, wrapErr(err, "read last alert firing")
	}

	parsed, err := time.Parse(time.RFC3339, firedAt)
	if err != nil {
		// SQLite's CURRENT_TIMESTAMP format.
		parsed, err = time.Parse("2006-01-02 15:04:05", firedAt)
		if err != nil {
			return time.Time{}, apperr.Internal("parse alert firing time", err)
		}
	}
	return parsed.UTC(), nil
}

// UpdateAlertLastFired sets last_fired_at on the alert row.
func (s *AlertStore) UpdateAlertLastFired(ctx context.Context, alertID string, firedAt time.Time) error {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return apperr.InvalidInput("invalid alert ID", err)
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE alerts SET last_fired_at = ? WHERE id = ?`,
		firedAt.UTC().Format(time.RFC3339Nano),
		rowID,
	)
	return wrapErr(err, "update alert last fired")
}
