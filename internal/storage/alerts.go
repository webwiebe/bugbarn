package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

const alertIDPrefix = "alert-"

func (s *Store) ListAlerts(ctx context.Context) ([]Alert, error) {
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	var query string
	var args []any
	if projectID != 0 {
		query = `
SELECT
	a.id,
	a.name,
	a.enabled,
	a.severity,
	a.rule_json,
	a.webhook_url,
	a.email_to,
	a.condition,
	a.param,
	a.threshold,
	a.cooldown_minutes,
	a.last_fired_at,
	a.created_at,
	a.updated_at,
	COALESCE(p.slug, '') AS project_slug
FROM alerts a
LEFT JOIN projects p ON p.id = a.project_id
WHERE a.project_id = ?
ORDER BY a.id DESC`
		args = []any{projectID}
	} else {
		query = `
SELECT
	a.id,
	a.name,
	a.enabled,
	a.severity,
	a.rule_json,
	a.webhook_url,
	a.email_to,
	a.condition,
	a.param,
	a.threshold,
	a.cooldown_minutes,
	a.last_fired_at,
	a.created_at,
	a.updated_at,
	COALESCE(p.slug, '') AS project_slug
FROM alerts a
LEFT JOIN projects p ON p.id = a.project_id
ORDER BY a.id DESC`
	}

	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		item, err := scanAlertWithProject(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, item)
	}
	return alerts, rows.Err()
}

func (s *Store) GetAlert(ctx context.Context, alertID string) (Alert, error) {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return Alert{}, err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	const sel = `
SELECT
	a.id,
	a.name,
	a.enabled,
	a.severity,
	a.rule_json,
	a.webhook_url,
	a.email_to,
	a.condition,
	a.param,
	a.threshold,
	a.cooldown_minutes,
	a.last_fired_at,
	a.created_at,
	a.updated_at,
	COALESCE(p.slug, '') AS project_slug
FROM alerts a
LEFT JOIN projects p ON p.id = a.project_id`

	var row *sql.Row
	if projectID != 0 {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE a.project_id = ? AND a.id = ?`, projectID, rowID)
	} else {
		row = s.readDB().QueryRowContext(ctx, sel+`
WHERE a.id = ?`, rowID)
	}
	alert, err := scanAlertWithProject(row)
	if err != nil {
		return Alert{}, wrapNotFound(err, "alert not found")
	}
	return alert, nil
}

func (s *Store) CreateAlert(ctx context.Context, alert Alert) (Alert, error) {
	if strings.TrimSpace(alert.Name) == "" {
		return Alert{}, apperr.InvalidInput("alert name is required", nil)
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	now := time.Now().UTC()
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO alerts (
	project_id,
	name,
	enabled,
	severity,
	rule_json,
	webhook_url,
	email_to,
	condition,
	param,
	threshold,
	cooldown_minutes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID,
		alert.Name,
		boolToInt(alert.Enabled),
		alert.Severity,
		mustMarshalObject(alert.Rule),
		alert.WebhookURL,
		alert.EmailTo,
		alert.Condition,
		alert.Param,
		alert.Threshold,
		alert.CooldownMinutes,
	)
	if err != nil {
		return Alert{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Alert{}, err
	}
	alert.ID = formatID(alertIDPrefix, id)
	alert.CreatedAt = now
	alert.UpdatedAt = now
	return alert, nil
}

func (s *Store) UpdateAlert(ctx context.Context, alertID string, alert Alert) (Alert, error) {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return Alert{}, apperr.InvalidInput("invalid alert ID", err)
	}
	if strings.TrimSpace(alert.Name) == "" {
		return Alert{}, apperr.InvalidInput("alert name is required", nil)
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE alerts
SET name = ?, enabled = ?, severity = ?, rule_json = ?,
    webhook_url = ?, email_to = ?, condition = ?, param = ?, threshold = ?, cooldown_minutes = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ?`,
		alert.Name,
		boolToInt(alert.Enabled),
		alert.Severity,
		mustMarshalObject(alert.Rule),
		alert.WebhookURL,
		alert.EmailTo,
		alert.Condition,
		alert.Param,
		alert.Threshold,
		alert.CooldownMinutes,
		projectID,
		rowID,
	); err != nil {
		return Alert{}, err
	}
	return s.GetAlert(ctx, alertID)
}

func (s *Store) DeleteAlert(ctx context.Context, alertID string) error {
	rowID, err := parseID(alertIDPrefix, alertID)
	if err != nil {
		return err
	}
	projectID, ok := ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM alerts WHERE project_id = ? AND id = ?`, projectID, rowID)
	return err
}

func scanAlertWithProject(scanner interface {
	Scan(dest ...any) error
}) (Alert, error) {
	var (
		id          int64
		item        Alert
		ruleRaw     []byte
		lastFiredAt string
		createdAt   string
		updatedAt   string
		enabled     int
	)
	if err := scanner.Scan(
		&id,
		&item.Name,
		&enabled,
		&item.Severity,
		&ruleRaw,
		&item.WebhookURL,
		&item.EmailTo,
		&item.Condition,
		&item.Param,
		&item.Threshold,
		&item.CooldownMinutes,
		&lastFiredAt,
		&createdAt,
		&updatedAt,
		&item.ProjectSlug,
	); err != nil {
		return Alert{}, err
	}
	item.ID = formatID(alertIDPrefix, id)
	item.Enabled = enabled != 0
	item.LastFiredAt, _ = parseTime(lastFiredAt)
	item.CreatedAt, _ = parseTime(createdAt)
	item.UpdatedAt, _ = parseTime(updatedAt)
	if len(ruleRaw) > 0 {
		if err := json.Unmarshal(ruleRaw, &item.Rule); err != nil {
			return Alert{}, err
		}
	}
	if item.Rule == nil {
		item.Rule = map[string]any{}
	}
	return item, nil
}

func mustMarshalObject(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
