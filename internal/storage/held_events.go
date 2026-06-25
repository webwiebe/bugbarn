package storage

import (
	"context"
	"errors"
	"time"
)

// Held-event kinds mirror the queue item kinds; kept as storage constants so the
// persist pipeline does not need to import the queue package.
const (
	HeldKindEvent = "event"
	HeldKindLog   = "log"
)

// HeldRecord is a raw ingest payload parked for a project that is pending admin
// approval. BodyBase64 is the exact payload received, so replay after approval
// is identical to live ingest.
type HeldRecord struct {
	ID          int64
	ProjectID   int64
	Slug        string
	Kind        string
	IngestID    string
	ReceivedAt  time.Time
	ContentType string
	BodyBase64  string
	CreatedAt   time.Time
}

// HoldEvent parks a raw ingest payload for a pending project. It is a write and
// fails on a read-only store (readers never hold — they forward to the writer).
func (s *HeldEventStore) HoldEvent(ctx context.Context, h HeldRecord) error {
	if s == nil || s.db == nil {
		return errors.New("storage is read-only")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO held_events (project_id, slug, kind, ingest_id, received_at, content_type, body_base64, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ProjectID, h.Slug, h.Kind, h.IngestID,
		formatTime(h.ReceivedAt), h.ContentType, h.BodyBase64, formatTime(time.Now().UTC()),
	)
	if err != nil {
		return wrapErr(err, "hold event")
	}
	return nil
}

// ListHeldByProject returns up to limit held records for a project, oldest
// first, so the backlog replays in arrival order.
func (s *HeldEventStore) ListHeldByProject(ctx context.Context, projectID int64, limit int) ([]HeldRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.readDB().QueryContext(ctx, `
SELECT id, project_id, slug, kind, ingest_id, received_at, content_type, body_base64, created_at
FROM held_events WHERE project_id = ? ORDER BY id ASC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var held []HeldRecord
	for rows.Next() {
		var h HeldRecord
		var receivedAt, createdAt string
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Slug, &h.Kind, &h.IngestID, &receivedAt, &h.ContentType, &h.BodyBase64, &createdAt); err != nil {
			return nil, err
		}
		h.ReceivedAt, _ = parseTime(receivedAt)
		h.CreatedAt, _ = parseTime(createdAt)
		held = append(held, h)
	}
	return held, rows.Err()
}

// DeleteHeldEvent removes a single held record once it has been replayed.
func (s *HeldEventStore) DeleteHeldEvent(ctx context.Context, id int64) error {
	if s == nil || s.db == nil {
		return errors.New("storage is read-only")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM held_events WHERE id = ?`, id)
	return err
}

// CountHeldByProject returns how many records are currently held for a project.
func (s *HeldEventStore) CountHeldByProject(ctx context.Context, projectID int64) (int, error) {
	var n int
	err := s.readDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM held_events WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}
