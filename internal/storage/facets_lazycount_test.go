package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// The counter must issue no query until something actually asks for the count.
// A nil *sql.Tx would panic on any query, so surviving construction and inc()
// proves nothing was executed.
func TestFacetKeyCounterIssuesNoQueryUntilAsked(t *testing.T) {
	t.Parallel()

	c := lazyFacetKeyCount(context.Background(), nil, 1)
	c.inc()
	c.inc()
	if c.loaded {
		t.Fatal("counter reported loaded without get() ever being called")
	}
}

// newFacetFixture persists a real event so facet rows satisfy their FKs, and
// returns the event and issue row IDs to hang facets off.
func newFacetFixture(t *testing.T, store *Store) (evRowID, issueRowID int64) {
	t.Helper()
	ctx := context.Background()
	_, ev, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 7, 16, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "lazy facet count fixture",
		Exception:  event.Exception{Type: "LazyErr", Message: "lazy facet count fixture"},
	}))
	if err != nil {
		t.Fatalf("persist fixture event: %v", err)
	}
	evRowID, err = parseID(eventIDPrefix, ev.ID)
	if err != nil {
		t.Fatalf("parse event id: %v", err)
	}
	issueRowID, err = store.IssueRowIDByDisplayID(ctx, ev.IssueID)
	if err != nil {
		t.Fatalf("resolve issue row id: %v", err)
	}
	return evRowID, issueRowID
}

func TestFacetKeyCounterLoadsOnceThenCountsInMemory(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	evID, issueID := newFacetFixture(t, store)
	if err := store.PersistFacets(ctx, evID, issueID, map[string]string{"env": "prod", "region": "eu"}); err != nil {
		t.Fatalf("seed facets: %v", err)
	}

	projectID := store.defaultProjectID

	// Persisting the fixture event extracts its own facets too, so take the
	// truth from the database rather than assuming a literal.
	var want int
	if err := store.db.QueryRow(
		`SELECT COUNT(DISTINCT facet_key) FROM event_facets WHERE project_id = ?`, projectID,
	).Scan(&want); err != nil {
		t.Fatalf("baseline count: %v", err)
	}
	if want < 2 {
		t.Fatalf("baseline distinct keys = %d, expected at least the 2 seeded", want)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	c := lazyFacetKeyCount(ctx, tx, projectID)
	n, err := c.get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if n != want {
		t.Fatalf("first get = %d, want %d", n, want)
	}
	if !c.loaded {
		t.Fatal("counter should be loaded after get()")
	}

	// A newly inserted key is tracked in memory; get() must not re-query.
	c.inc()
	n2, err := c.get()
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if n2 != want+1 {
		t.Errorf("after inc, get = %d, want %d (tracked in memory, not re-queried)", n2, want+1)
	}
}

// The point of the change: an event whose facet keys already exist must persist
// normally without the cap ever being consulted, and both rows must land.
func TestPersistFacetsWithExistingKeysStillPersists(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	evID, issueID := newFacetFixture(t, store)
	if err := store.PersistFacets(ctx, evID, issueID, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("seed PersistFacets: %v", err)
	}
	// Same key, new value: no new key, so the cap is never in play.
	if err := store.PersistFacets(ctx, evID, issueID, map[string]string{"env": "staging"}); err != nil {
		t.Fatalf("PersistFacets with existing key: %v", err)
	}

	var vals int
	if err := store.db.QueryRow(
		`SELECT COUNT(DISTINCT facet_value) FROM event_facets WHERE facet_key = 'env'`,
	).Scan(&vals); err != nil {
		t.Fatalf("count values: %v", err)
	}
	if vals != 2 {
		t.Errorf("distinct values for 'env' = %d, want 2 (both persisted)", vals)
	}
}
