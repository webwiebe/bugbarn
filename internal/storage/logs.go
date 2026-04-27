package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// InsertLogEntries inserts a batch of log entries for a project.
// After insert, trims to the 10,000 most recent entries for that project.
func (s *Store) InsertLogEntries(ctx context.Context, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO log_entries (project_id, received_at, level_num, level, message, data_json)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		dataJSON := "{}"
		if len(e.Data) > 0 {
			b, err := json.Marshal(e.Data)
			if err == nil {
				dataJSON = string(b)
			}
		}
		receivedAt := e.ReceivedAt
		if receivedAt.IsZero() {
			receivedAt = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx, e.ProjectID, receivedAt.UTC().Format(time.RFC3339Nano), e.LevelNum, e.Level, e.Message, dataJSON); err != nil {
			return err
		}
	}

	// Trim to 10,000 most recent entries per project when over the cap.
	projectID := entries[0].ProjectID
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_entries WHERE project_id = ?`, projectID).Scan(&count); err != nil {
		return err
	}
	if count > 10000 {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM log_entries WHERE project_id = ? AND id <= (
				SELECT MIN(id) FROM (
					SELECT id FROM log_entries WHERE project_id = ? ORDER BY id DESC LIMIT 10000
				)
			)
		`, projectID, projectID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListLogEntries returns up to limit entries for a project (or all projects when projectID=0), newest first.
// Optional filters: levelMin (numeric pino level, 0 = no filter), q (substring match on message),
// beforeID (cursor, 0 = no cursor).
func (s *Store) ListLogEntries(ctx context.Context, projectID int64, levelMin int, q string, limit int, beforeID int64) ([]LogEntry, error) {
	var args []any
	var conditions []string

	if projectID != 0 {
		conditions = append(conditions, "le.project_id = ?")
		args = append(args, projectID)
	}
	if levelMin > 0 {
		conditions = append(conditions, "le.level_num >= ?")
		args = append(args, levelMin)
	}
	if q != "" {
		conditions = append(conditions, "le.message LIKE ?")
		args = append(args, fmt.Sprintf("%%%s%%", q))
	}
	if beforeID > 0 {
		conditions = append(conditions, "le.id < ?")
		args = append(args, beforeID)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT le.id, le.project_id, le.received_at, le.level_num, le.level, le.message, le.data_json, COALESCE(p.slug, '') AS project_slug
		FROM log_entries le
		LEFT JOIN projects p ON p.id = le.project_id
		%s
		ORDER BY le.id DESC
		LIMIT ?
	`, whereClause)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var receivedAt string
		var dataJSON string
		if err := rows.Scan(&e.ID, &e.ProjectID, &receivedAt, &e.LevelNum, &e.Level, &e.Message, &dataJSON, &e.ProjectSlug); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, receivedAt); err == nil {
			e.ReceivedAt = t
		} else if t, err := time.Parse(time.RFC3339, receivedAt); err == nil {
			e.ReceivedAt = t
		}
		if dataJSON != "" && dataJSON != "{}" {
			var data map[string]any
			if err := json.Unmarshal([]byte(dataJSON), &data); err == nil {
				e.Data = data
			}
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
