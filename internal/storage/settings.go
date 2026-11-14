package storage

import (
	"context"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (s *SettingsStore) GetSettings(ctx context.Context) (map[string]string, error) {
	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	rows, err := s.readDB().QueryContext(ctx, `
SELECT key, value
FROM settings
WHERE project_id = ?`,
		projectID,
	)
	if err != nil {
		return nil, wrapErr(err, "read settings")
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, wrapErr(err, "read settings")
		}
		out[key] = value
	}
	return out, wrapErr(rows.Err(), "read settings")
}

func (s *SettingsStore) UpdateSettings(ctx context.Context, values map[string]string) error {
	projectID, ok := domain.ProjectIDFromContext(ctx)
	if !ok || projectID <= 0 {
		projectID = s.defaultProjectID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrapErr(err, "update settings")
	}
	defer tx.Rollback()

	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (project_id, key, value, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(project_id, key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			projectID,
			key,
			value,
		); err != nil {
			return wrapErr(err, "update settings")
		}
	}

	return wrapErr(tx.Commit(), "update settings")
}
