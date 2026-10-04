package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// StoredDetectionRule is one row of detection_rules: the rule JSON as the API
// accepted it. The storage layer does not parse it; internal/detect owns the
// format.
type StoredDetectionRule struct {
	ID        string
	JSON      string
	UpdatedAt time.Time
}

// ListDetectionRules returns every stored rule override and custom rule, by id.
func (s *DetectionRuleStore) ListDetectionRules(ctx context.Context) ([]StoredDetectionRule, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id, rule_json, updated_at FROM detection_rules ORDER BY id`)
	if err != nil {
		return nil, wrapErr(err, "list detection rules")
	}
	defer rows.Close()
	var out []StoredDetectionRule
	for rows.Next() {
		var r StoredDetectionRule
		var updated string
		if err := rows.Scan(&r.ID, &r.JSON, &updated); err != nil {
			return nil, wrapErr(err, "scan detection rule")
		}
		r.UpdatedAt, _ = parseTime(updated)
		out = append(out, r)
	}
	return out, wrapErr(rows.Err(), "list detection rules")
}

// UpsertDetectionRule stores the rule JSON under id, replacing any earlier row.
func (s *DetectionRuleStore) UpsertDetectionRule(ctx context.Context, id, ruleJSON string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO detection_rules (id, rule_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(id) DO UPDATE SET rule_json = excluded.rule_json, updated_at = excluded.updated_at`, id, ruleJSON, formatTime(time.Now()))
	return wrapErr(err, "upsert detection rule")
}

// DeleteDetectionRule removes the stored row for id. It returns NotFound when
// there is none.
func (s *DetectionRuleStore) DeleteDetectionRule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM detection_rules WHERE id = ?`, id)
	if err != nil {
		return wrapErr(err, "delete detection rule")
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return apperr.NotFound("detection rule not found", sql.ErrNoRows)
	}
	return nil
}
