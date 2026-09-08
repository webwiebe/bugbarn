package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedEventsAt inserts n events for the default project, all received at the
// given time, and returns the issue they belong to.
func seedEventsAt(t *testing.T, s *Store, receivedAt time.Time, n int) int64 {
	t.Helper()
	ctx := context.Background()
	db := s.DB()
	pid := s.DefaultProjectID()

	fp := fmt.Sprintf("fp-%d", receivedAt.UnixNano())
	res, err := db.ExecContext(ctx, `
		INSERT INTO issues (project_id, fingerprint, fingerprint_material, title, normalized_title,
			exception_type, first_seen, last_seen, event_count, representative_event_json, issue_number)
		VALUES (?, ?, '', 'issue', 'issue', 'Error', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, '{}', 1)`,
		pid, fp, n)
	if err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	ts := receivedAt.UTC().Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO events (project_id, issue_id, fingerprint, received_at, observed_at, severity, message, event_json)
			VALUES (?, ?, 'fp', ?, ?, 'error', 'test', '{}')`,
			pid, issueID, ts, ts); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	// The facet set is per issue, not per event: one row however many events the
	// issue has.
	if _, err := db.ExecContext(ctx, `
		INSERT OR IGNORE INTO issue_facets (project_id, issue_id, facet_key, facet_value)
		VALUES (?, ?, 'env', 'prod')`, pid, issueID); err != nil {
		t.Fatalf("insert facet: %v", err)
	}
	return issueID
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDeleteEventsBeforeRemovesOnlyExpiredEvents(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seedEventsAt(t, s, now.AddDate(0, 0, -40), 5) // expired
	seedEventsAt(t, s, now.AddDate(0, 0, -2), 3)  // fresh

	cutoff := now.AddDate(0, 0, -30)
	deleted, err := s.DeleteEventsBefore(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
	if got := countRows(t, s, "events"); got != 3 {
		t.Errorf("events remaining = %d, want 3 (the fresh ones)", got)
	}
}

// Facets outlive the events they were observed on. Since migration 00013 they
// are a per-issue projection with no event_id, so expiring an issue's events
// must not take its environments and hosts with them: the issue survives, and
// so must the facets that make it findable.
func TestDeleteEventsBeforeLeavesFacetsIntact(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seedEventsAt(t, s, now.AddDate(0, 0, -40), 4)
	seedEventsAt(t, s, now.AddDate(0, 0, -1), 2)

	if got := countRows(t, s, "issue_facets"); got != 2 {
		t.Fatalf("facets before = %d, want 2 (one per issue)", got)
	}
	if _, err := s.DeleteEventsBefore(ctx, now.AddDate(0, 0, -30), 100); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := countRows(t, s, "issue_facets"); got != 2 {
		t.Errorf("facets after = %d, want 2 — the projection is not tied to events", got)
	}
}

// Issues are a durable record of what happened; expiring an issue's events must
// not rewrite history by touching its lifetime event_count.
func TestDeleteEventsBeforeLeavesIssuesIntact(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	issueID := seedEventsAt(t, s, now.AddDate(0, 0, -60), 7)

	if _, err := s.DeleteEventsBefore(ctx, now.AddDate(0, 0, -30), 100); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int
	var eventCount int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(event_count) FROM issues WHERE id = ?`, issueID).Scan(&count, &eventCount); err != nil {
		t.Fatalf("query issue: %v", err)
	}
	if count != 1 {
		t.Fatalf("issue rows = %d, want 1 — the issue must survive its events", count)
	}
	if eventCount != 7 {
		t.Errorf("issue.event_count = %d, want 7 — the lifetime counter must not be rewritten", eventCount)
	}
}

func TestDeleteEventsBeforeHonorsBatchLimit(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -30)

	seedEventsAt(t, s, now.AddDate(0, 0, -45), 10)

	deleted, err := s.DeleteEventsBefore(ctx, cutoff, 4)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4 (the batch limit)", deleted)
	}
	if got := countRows(t, s, "events"); got != 6 {
		t.Errorf("events remaining = %d, want 6", got)
	}

	// A limit of 0 or less is a no-op rather than an unbounded delete.
	deleted, err = s.DeleteEventsBefore(ctx, cutoff, 0)
	if err != nil {
		t.Fatalf("delete with zero limit: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d with limit 0, want 0", deleted)
	}
	if got := countRows(t, s, "events"); got != 6 {
		t.Errorf("events remaining = %d after zero-limit delete, want 6", got)
	}
}

func TestCountEventsBefore(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seedEventsAt(t, s, now.AddDate(0, 0, -90), 6)
	seedEventsAt(t, s, now.AddDate(0, 0, -3), 2)

	n, err := s.CountEventsBefore(ctx, now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 6 {
		t.Errorf("CountEventsBefore = %d, want 6", n)
	}
}

// Retention is writer-only, but a read-only store must fail loudly rather than
// panic on a nil write connection if it is ever wired up by mistake.
func TestDeleteEventsBeforeRejectsReadOnlyStore(t *testing.T) {
	var s *EventStore
	n, err := s.DeleteEventsBefore(context.Background(), time.Now(), 100)
	if err == nil {
		t.Fatal("expected an error from a read-only store, got nil")
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0", n)
	}
}

// The sweep must have no facet work left to do. event_facets carried a
// per-event ON DELETE CASCADE that made every deleted event a child lookup, and
// needed idx_event_facets_event to keep that from being a full scan. Migration
// 00013 removed the table and with it the cascade; if a per-event facet table
// ever comes back, this fails and the index question comes back with it.
func TestNoPerEventFacetTable(t *testing.T) {
	s := mustOpenStore(t)
	var name string
	err := s.DB().QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='event_facets'`).Scan(&name)
	if err == nil {
		t.Fatal("event_facets is back: the retention sweep now cascades a facet delete per event again")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("look up event_facets: %v", err)
	}
}

// Existence alone is not enough here, so this asserts the plan instead.
//
// The batch subquery has to be able to stop at LIMIT. When it cannot, a short
// batch — which in steady state is *every* batch, since far fewer events expire
// per hour than the batch size — reads the whole table to prove nothing more
// matches. On production that was 98 seconds of unbroken hold on the single
// write connection, every hour, which is what starved ingest and dropped
// accepted log batches (BS2-98).
//
// Ordering is the load-bearing part and the reason a plain existence check
// would miss a regression: with idx_events_received_at in place but ORDER BY id
// restored, SQLite goes right back to `SCAN events`, because it would have to
// sort every match to satisfy that order. The index only pays off while the
// query asks for the order the index already has.
func TestDeleteEventsBeforePlanUsesReceivedAtIndex(t *testing.T) {
	s := mustOpenStore(t)
	seedEventsAt(t, s, time.Now().AddDate(0, 0, -40), 5)

	rows, err := s.DB().QueryContext(context.Background(), `
		EXPLAIN QUERY PLAN
		DELETE FROM events WHERE id IN (
			SELECT id FROM events WHERE received_at < ? ORDER BY received_at ASC, id ASC LIMIT ?
		)`, time.Now().UTC().Format(time.RFC3339Nano), 2000)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}

	var usesIndex bool
	for _, step := range plan {
		if strings.Contains(step, "idx_events_received_at") {
			usesIndex = true
		}
		// A bare table scan is the exact regression this guards. Steps that
		// name an index are fine however they are phrased.
		if strings.Contains(step, "SCAN events") && !strings.Contains(step, "INDEX") {
			t.Errorf("retention batch falls back to a full table scan: %q\nfull plan: %v", step, plan)
		}
	}
	if !usesIndex {
		t.Errorf("retention batch does not use idx_events_received_at; plan: %v", plan)
	}
}

// Deleting oldest-first must stay oldest-first now that the ORDER BY drives off
// received_at rather than id.
func TestDeleteEventsBeforeRemovesOldestFirst(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seedEventsAt(t, s, now.AddDate(0, 0, -60), 3) // oldest
	seedEventsAt(t, s, now.AddDate(0, 0, -45), 3) // middle

	// Budget for only the three oldest.
	if _, err := s.DeleteEventsBefore(ctx, now.AddDate(0, 0, -30), 3); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var oldest int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE received_at < ?`,
		now.AddDate(0, 0, -50).Format(time.RFC3339Nano)).Scan(&oldest); err != nil {
		t.Fatalf("count oldest: %v", err)
	}
	if oldest != 0 {
		t.Errorf("%d of the oldest events survived a budgeted batch; retention must expire oldest-first", oldest)
	}
	if got := countRows(t, s, "events"); got != 3 {
		t.Errorf("events remaining = %d, want 3", got)
	}
}
