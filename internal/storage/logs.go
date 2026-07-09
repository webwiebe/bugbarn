package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// logRetentionCap is the soft per-project cap on retained log entries.
const logRetentionCap = 10000

// logInsertChunk is the max rows per multi-row INSERT statement. At 6 columns
// per row this stays well under SQLite's 999 bound parameter limit.
const logInsertChunk = 100

// logTrimInterval amortizes the retention trim: the COUNT + DELETE runs roughly
// once per this many insert batches instead of on every insert, so the typical
// log write holds the single shared writer connection for as little as possible.
const logTrimInterval = 32

// InsertLogEntries inserts a batch of log entries for a project.
// Periodically trims to the logRetentionCap most recent entries for that project.
//
// The write path honors the caller's context: the single writer connection is
// shared with event ingestion, so if a client disconnects we must abandon the
// insert promptly rather than hold the connection. A short ceiling also bounds
// how long any one batch can occupy the writer.
func (s *LogStore) InsertLogEntries(ctx context.Context, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for start := 0; start < len(entries); start += logInsertChunk {
		end := start + logInsertChunk
		if end > len(entries) {
			end = len(entries)
		}
		if err := insertLogChunk(ctx, tx, entries[start:end]); err != nil {
			return err
		}
	}

	// Trim to the retention cap. The COUNT + DELETE is pure overhead on every
	// insert, so for the common case (a flood of small batches) we amortize it
	// across logTrimInterval batches; overshooting the soft cap by a few batches
	// between trims is harmless. A batch large enough to breach the cap on its
	// own always trims so the cap can't be blown past in a single call.
	if len(entries) >= logTrimInterval || s.logInsertCount.Add(1)%logTrimInterval == 0 {
		if err := trimLogRetention(ctx, tx, entries[0].ProjectID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// insertLogChunk writes one multi-row INSERT for a slice of log entries that is
// already sized within the bound-parameter limit.
func insertLogChunk(ctx context.Context, tx *sql.Tx, chunk []LogEntry) error {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO log_entries (project_id, received_at, level_num, level, message, data_json) VALUES `)
	args := make([]any, 0, len(chunk)*6)
	for i, e := range chunk {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`(?, ?, ?, ?, ?, ?)`)

		dataJSON := "{}"
		if len(e.Data) > 0 {
			if b, err := json.Marshal(e.Data); err == nil {
				dataJSON = string(b)
			}
		}
		receivedAt := e.ReceivedAt
		if receivedAt.IsZero() {
			receivedAt = time.Now().UTC()
		}
		args = append(args, e.ProjectID, receivedAt.UTC().Format(time.RFC3339Nano), e.LevelNum, e.Level, e.Message, dataJSON)
	}
	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return err
	}
	return nil
}

// trimLogRetention deletes the oldest log entries for a project beyond the
// retention cap.
func trimLogRetention(ctx context.Context, tx *sql.Tx, projectID int64) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_entries WHERE project_id = ?`, projectID).Scan(&count); err != nil {
		return err
	}
	if count > logRetentionCap {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM log_entries WHERE project_id = ? AND id <= (
				SELECT MIN(id) FROM (
					SELECT id FROM log_entries WHERE project_id = ? ORDER BY id DESC LIMIT ?
				)
			)
		`, projectID, projectID, logRetentionCap); err != nil {
			return err
		}
	}
	return nil
}

// ListLogEntries returns up to limit entries for a project (or all projects when projectID=0), newest first.
// Optional filters: levelMin (numeric pino level, 0 = no filter), q (substring match on message),
// beforeID (cursor, 0 = no cursor).
func (s *LogStore) ListLogEntries(
	ctx context.Context, projectID int64, levelMin int, q string, limit int, beforeID int64,
) ([]LogEntry, error) {
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

	rows, err := s.readDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapErr(err, "list log entries")
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var receivedAt string
		var dataJSON string
		if err := rows.Scan(&e.ID, &e.ProjectID, &receivedAt, &e.LevelNum, &e.Level, &e.Message, &dataJSON, &e.ProjectSlug); err != nil {
			return nil, wrapErr(err, "scan log entry")
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
		return nil, wrapErr(err, "list log entries")
	}
	return entries, nil
}
