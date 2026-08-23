package storage

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// DeleteEventsBefore removes at most limit events received before cutoff,
// oldest first, and reports how many rows it deleted. A return value below
// limit means the backlog for this cutoff is drained.
//
// Deleting in bounded batches is the whole point. SQLite serializes writers at
// the file level and this database has exactly one write connection shared with
// event ingestion, so a single unbounded `DELETE FROM events WHERE received_at
// < ?` over a multi-million-row backlog would hold that connection — and
// therefore stall all ingest — for as long as it ran, while pushing every
// deleted page into a WAL that only the 60s checkpoint loop can truncate. The
// caller sleeps between batches so ingest gets the writer back in between.
//
// event_facets rows are removed by the ON DELETE CASCADE on
// event_facets.event_id, which migration 00011 made index-backed; without that
// index this loop degrades to a full scan of event_facets per deleted event.
//
// issues are deliberately left alone. issues.event_count is a lifetime counter
// maintained by the ingest path, not a count of retained rows, so trimming
// events must not touch it: an issue that fired 1.8M times still fired 1.8M
// times after its old events age out. The issue row remains as the durable
// record; only the individual event payloads expire.
func (s *EventStore) DeleteEventsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	// Read-only store: no write connection to delete through. The retention
	// worker only ever starts on the writer, so reaching this means a wiring
	// mistake — report it rather than panicking on a nil *sql.DB, matching the
	// guard RunPeriodicCheckpoint uses for the same situation.
	if s == nil || s.db == nil {
		return 0, apperr.Internal("delete events before cutoff: store is read-only", nil)
	}
	// Selecting ids by the id order rather than by received_at keeps the
	// subquery on the primary key: received_at is RFC3339Nano text, so a
	// lexicographic comparison is a valid chronological one, and
	// idx_events_project_received_at cannot serve a cross-project scan anyway.
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM events WHERE id IN (
			SELECT id FROM events WHERE received_at < ? ORDER BY id ASC LIMIT ?
		)`,
		cutoff.UTC().Format(time.RFC3339Nano), limit,
	)
	if err != nil {
		return 0, wrapErr(err, "delete events before cutoff")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrapErr(err, "delete events before cutoff")
	}
	return n, nil
}

// CountEventsBefore reports how many events are older than cutoff. It is used
// only for reporting the remaining backlog, never as a gate on deleting: it is
// a full scan of the retention window and must not run on every batch.
func (s *EventStore) CountEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := s.readDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE received_at < ?`,
		cutoff.UTC().Format(time.RFC3339Nano),
	).Scan(&n)
	if err != nil {
		return 0, wrapErr(err, "count events before cutoff")
	}
	return n, nil
}
