package alert

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Repository defines the data access contract for alert rules and firings.
type Repository interface {
	ListForProject(ctx context.Context, projectID int64) ([]Rule, error)
	RecordFiring(ctx context.Context, alertID, issueID string) error
	LastFiring(ctx context.Context, alertID, issueID string) (time.Time, error)
	UpdateLastFired(ctx context.Context, alertID string, firedAt time.Time) error
}

// SQLiteRepository implements Repository backed by a *sql.DB (SQLite).
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a new SQLiteRepository using the given database connection.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// ListForProject returns all enabled alert rules for a given project.
func (r *SQLiteRepository) ListForProject(ctx context.Context, projectID int64) ([]Rule, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT
	id,
	name,
	enabled,
	webhook_url,
	condition,
	threshold,
	cooldown_minutes,
	last_fired_at,
	created_at,
	updated_at
FROM alerts
WHERE project_id = ?
ORDER BY id DESC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rule.ProjectID = projectID
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// RecordFiring inserts a new firing record for the alert/issue pair.
func (r *SQLiteRepository) RecordFiring(ctx context.Context, alertID, issueID string) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO alert_firings (alert_id, issue_id, fired_at)
VALUES (?, ?, CURRENT_TIMESTAMP)`,
		alertID,
		issueID,
	)
	return err
}

// LastFiring returns the timestamp of the most recent firing for a given alert/issue pair.
// Returns a zero time.Time if no firing has been recorded.
func (r *SQLiteRepository) LastFiring(ctx context.Context, alertID, issueID string) (time.Time, error) {
	var firedAt string
	err := r.db.QueryRowContext(ctx, `
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
		return time.Time{}, err
	}

	parsed, err := time.Parse(time.RFC3339, firedAt)
	if err != nil {
		// Try alternate SQLite timestamp format.
		parsed, err = time.Parse("2006-01-02 15:04:05", firedAt)
		if err != nil {
			return time.Time{}, err
		}
	}
	return parsed.UTC(), nil
}

// UpdateLastFired updates the last_fired_at column on the alert row.
func (r *SQLiteRepository) UpdateLastFired(ctx context.Context, alertID string, firedAt time.Time) error {
	rowID, err := parseAlertID(alertID)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
UPDATE alerts SET last_fired_at = ? WHERE id = ?`,
		firedAt.UTC().Format(time.RFC3339Nano),
		rowID,
	)
	return err
}

func scanRule(scanner interface {
	Scan(dest ...any) error
}) (Rule, error) {
	var (
		id              int64
		rule            Rule
		enabled         int
		webhookURL      string
		condition       string
		threshold       int
		cooldownMinutes int
		lastFiredAt     string
		createdAt       string
		updatedAt       string
	)
	if err := scanner.Scan(
		&id,
		&rule.Name,
		&enabled,
		&webhookURL,
		&condition,
		&threshold,
		&cooldownMinutes,
		&lastFiredAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Rule{}, err
	}
	rule.ID = formatAlertID(id)
	rule.Enabled = enabled != 0
	rule.WebhookURL = webhookURL
	rule.Condition = condition
	rule.Threshold = threshold
	rule.CooldownMinutes = cooldownMinutes
	rule.LastFiredAt, _ = parseAlertTime(lastFiredAt)
	rule.CreatedAt, _ = parseAlertTime(createdAt)
	rule.UpdatedAt, _ = parseAlertTime(updatedAt)
	return rule, nil
}

func formatAlertID(id int64) string {
	return "alert-" + strconv.FormatInt(id, 10)
}

func parseAlertID(id string) (int64, error) {
	id = strings.TrimPrefix(id, "alert-")
	n, err := strconv.ParseInt(strings.TrimLeft(id, "0"), 10, 64)
	if err != nil {
		// Try raw numeric string (e.g. "000001")
		n, err = strconv.ParseInt(id, 10, 64)
	}
	return n, err
}

func parseAlertTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, nil
}
