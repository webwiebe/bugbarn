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
// Facets are not touched. Since migration 00013 they live in issue_facets, a
// distinct per-issue projection with no link to any individual event, so the
// sweep no longer cascades a facet delete per event — and an issue keeps the
// environments and hosts it was ever seen with after its events age out.
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
	// Order by received_at, not by id. Both are chronological — received_at is
	// RFC3339Nano UTC text, so its lexicographic order is its time order — but
	// only received_at order lets migration 00012's idx_events_received_at
	// (received_at, id) serve the filter, the ordering and the projected column
	// at once, so the scan stops at LIMIT and never touches the table.
	//
	// Asking for id order instead is what this used to do, and it was the whole
	// problem: no index spans received_at across projects, so the planner walked
	// the primary key and evaluated every row. A batch that fills early gets away
	// with it; a short batch cannot, because returning fewer rows than the limit
	// means the search space was exhausted. Steady state is always a short batch
	// (~1,400 expiring per hour against a 2000 limit), so every sweep read the
	// whole table — 98s on production's 7.75GB, holding the single write
	// connection the entire time and starving ingest (BS2-98).
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM events WHERE id IN (
			SELECT id FROM events WHERE received_at < ? ORDER BY received_at ASC, id ASC LIMIT ?
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

// DeleteProjectEventsBefore is DeleteEventsBefore scoped to one project, for
// projects that carry their own retention window.
//
// It is a separate query rather than an optional predicate on the global one for
// an index reason. The global sweep is served by idx_events_received_at
// (received_at, id) from migration 00012, and adding `AND project_id = ?` to it
// would force the planner to evaluate rows it cannot use — which is exactly the
// full-table scan that made the hourly sweep take 98 seconds and starve ingest
// (BS2-98). This one is shaped for idx_events_project_received_at
// (project_id, received_at DESC, id DESC) from the initial schema, whose leading
// column is the project: the scan starts inside the project's partition and
// stops at LIMIT. SQLite walks a DESC index backwards happily, so the ASC
// ordering here still resolves to an index scan rather than a sort.
func (s *EventStore) DeleteProjectEventsBefore(ctx context.Context, projectID int64, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 || projectID <= 0 {
		return 0, nil
	}
	if s == nil || s.db == nil {
		return 0, apperr.Internal("delete project events before cutoff: store is read-only", nil)
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM events WHERE id IN (
			SELECT id FROM events WHERE project_id = ? AND received_at < ?
			ORDER BY received_at ASC, id ASC LIMIT ?
		)`,
		projectID, cutoff.UTC().Format(time.RFC3339Nano), limit,
	)
	if err != nil {
		return 0, wrapErr(err, "delete project events before cutoff")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrapErr(err, "delete project events before cutoff")
	}
	return n, nil
}
