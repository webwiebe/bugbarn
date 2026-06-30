package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestExtractFacets(t *testing.T) {
	t.Parallel()

	evt := event.Event{
		Severity: "ERROR",
		Resource: map[string]any{
			"host.name":              "web-01",
			"service.name":           "api",
			"telemetry.sdk.language": "go",
			"deployment.environment": "production",
		},
		Attributes: map[string]any{
			"http.route":          "/users/:id",
			"http.status_code":    float64(404),
			"http.method":         "GET",
			"user_agent.original": "Mozilla/5.0",
			"release":             "v2.3.0",
		},
	}

	facets := extractFacets(evt)

	assertFacet(t, facets, "host.name", "web-01")
	assertFacet(t, facets, "service.name", "api")
	assertFacet(t, facets, "telemetry.sdk.language", "go")
	assertFacet(t, facets, "deployment.environment", "production")
	assertFacet(t, facets, "http.route", "/users/:id")
	assertFacet(t, facets, "http.status_code", "404")
	assertFacet(t, facets, "http.method", "GET")
	assertFacet(t, facets, "user_agent.original", "Mozilla/5.0")
	assertFacet(t, facets, "severity", "ERROR")
	assertFacet(t, facets, "release", "v2.3.0")
	assertFacet(t, facets, "environment", "production")
}

func TestExtractFacetsEmptyEvent(t *testing.T) {
	t.Parallel()

	facets := extractFacets(event.Event{})
	if len(facets) != 0 {
		t.Fatalf("expected empty facets for empty event, got %v", facets)
	}
}

func TestExtractFacetsEnvironmentFallsBackToResource(t *testing.T) {
	t.Parallel()

	evt := event.Event{
		Resource: map[string]any{
			"deployment.environment": "staging",
		},
	}

	facets := extractFacets(evt)
	assertFacet(t, facets, "environment", "staging")
}

func TestPersistFacetsAndQueryAPIs(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	evt1 := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "facet test error",
		Exception:  event.Exception{Type: "FacetError", Message: "facet test error"},
		Resource:   map[string]any{"host.name": "web-01"},
		Attributes: map[string]any{"http.method": "GET"},
	})
	evt2 := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 5, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 5, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "facet test error",
		Exception:  event.Exception{Type: "FacetError", Message: "facet test error"},
		Resource:   map[string]any{"host.name": "web-02"},
		Attributes: map[string]any{"http.method": "POST"},
	})

	if _, _, _, _, err := store.PersistProcessedEvent(ctx, evt1); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, evt2); err != nil {
		t.Fatal(err)
	}

	projectID := store.DefaultProjectID()

	keys, err := store.ListFacetKeys(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one facet key")
	}
	hasHostName := false
	for _, k := range keys {
		if k == "host.name" {
			hasHostName = true
		}
	}
	if !hasHostName {
		t.Fatalf("expected 'host.name' in facet keys, got %v", keys)
	}

	values, err := store.ListFacetValues(ctx, projectID, "host.name")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("expected 2 distinct host.name values, got %d: %v", len(values), values)
	}
}

// TestPersistFacetsExistenceChecksUseIndex is a regression guard for the
// 2026-06-21 production outage. PersistFacets runs COUNT() existence checks on
// (project_id, facet_key[, facet_value]) for every facet of every ingested
// event. When no index covered those predicates beyond the project_id prefix,
// each check scanned the project's entire event_facets partition; as the table
// grew, ingest slowed to a crawl, the single SQLite writer connection was held
// for minutes per event, and the whole write pipeline wedged. This test asserts
// those queries resolve via an index search rather than a full table scan, so a
// future schema or query change that reintroduces the scan fails loudly here
// instead of silently in production.
func TestPersistFacetsExistenceChecksUseIndex(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// The exact predicates issued by PersistFacets (facets.go).
	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "key existence (project_id, facet_key)",
			query: `SELECT EXISTS(SELECT 1 FROM event_facets WHERE project_id = ? AND facet_key = ?)`,
			args:  []any{int64(1), "host.name"},
		},
		{
			name:  "value existence (project_id, facet_key, facet_value)",
			query: `SELECT EXISTS(SELECT 1 FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?)`,
			args:  []any{int64(1), "host.name", "web-01"},
		},
		{
			name:  "distinct values per key (project_id, facet_key)",
			query: `SELECT COUNT(DISTINCT facet_value) FROM event_facets WHERE project_id = ? AND facet_key = ?`,
			args:  []any{int64(1), "host.name"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := queryPlan(t, store, "EXPLAIN QUERY PLAN "+tc.query, tc.args...)
			// The outage plan was "SEARCH event_facets USING COVERING INDEX
			// idx_event_facets_issue (project_id=?)" — an index search, but
			// constrained only by project_id, so it still scanned the whole
			// project partition. The fix is an index that also constrains
			// facet_key. SQLite reports the constrained columns in the plan
			// detail, so requiring "facet_key" there proves the lookup is bound
			// past the project prefix and not re-scanning the partition.
			if !strings.Contains(plan, "INDEX") {
				t.Fatalf("query plan uses no index (full table scan) — the outage condition.\nquery: %s\nplan:  %s", tc.query, plan)
			}
			if !strings.Contains(plan, "facet_key") {
				t.Fatalf("query plan only constrains the project_id prefix and scans the project's whole facet partition — the outage condition.\nquery: %s\nplan:  %s", tc.query, plan)
			}
		})
	}
}

// TestFacetReadQueriesUseIndexes guards the project-scoped facet→issue filter:
// it needs issue_id in idx_event_facets_kv_issue to stay covering and avoid a
// per-row table lookup. Cross-project facet querying is intentionally not
// supported (migration 00009 dropped idx_event_facets_facet and the code paths),
// so there are no cross-project cases here.
func TestFacetReadQueriesUseIndexes(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cases := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:      "project-scoped issue filter by facet (covering)",
			query:     `SELECT DISTINCT issue_id FROM event_facets WHERE project_id = ? AND facet_key = ? AND facet_value = ?`,
			args:      []any{int64(1), "host.name", "web-01"},
			wantIndex: "idx_event_facets_kv_issue",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := queryPlan(t, store, "EXPLAIN QUERY PLAN "+tc.query, tc.args...)
			if !strings.Contains(plan, tc.wantIndex) {
				t.Fatalf("query does not use %s (likely a full table scan).\nquery: %s\nplan:  %s", tc.wantIndex, tc.query, plan)
			}
		})
	}
}

// queryPlan runs an EXPLAIN QUERY PLAN statement on the read pool and returns
// the concatenated detail of every step.
func queryPlan(t *testing.T, store *Store, explain string, args ...any) string {
	t.Helper()
	rows, err := store.roDB.QueryContext(context.Background(), explain, args...)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		// The final column is the human-readable "detail" of the plan step.
		if ns, ok := cells[len(cells)-1].(*sql.NullString); ok && ns.Valid {
			details = append(details, ns.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, " | ")
}

func TestListIssuesFilteredByFacets(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	evtA := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "error on web-01",
		Exception:  event.Exception{Type: "Error", Message: "error on web-01"},
		Resource:   map[string]any{"host.name": "web-01"},
	})
	evtB := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 5, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 5, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "error on web-02",
		Exception:  event.Exception{Type: "Error", Message: "error on web-02"},
		Resource:   map[string]any{"host.name": "web-02"},
	})

	issueA, _, _, _, err := store.PersistProcessedEvent(ctx, evtA)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = store.PersistProcessedEvent(ctx, evtB)
	if err != nil {
		t.Fatal(err)
	}

	filtered, err := store.ListIssuesFiltered(ctx, IssueFilter{
		Facets: map[string]string{"host.name": "web-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered issue, got %d", len(filtered))
	}
	if filtered[0].ID != issueA.ID {
		t.Fatalf("expected issueA (%s), got %s", issueA.ID, filtered[0].ID)
	}
}

func TestPersistFacetsCardinalityGuards(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create a real event so FK constraints are satisfied.
	baseEvt := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "cardinality guard test",
		Exception:  event.Exception{Type: "CardError", Message: "cardinality guard test"},
	})
	_, ev, _, _, err := store.PersistProcessedEvent(ctx, baseEvt)
	if err != nil {
		t.Fatal(err)
	}

	// Parse the row IDs back out.
	evRowID, err := parseID(eventIDPrefix, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	issueRowID, err := store.IssueRowIDByDisplayID(ctx, ev.IssueID)
	if err != nil {
		t.Fatal(err)
	}

	// Build maxFacetKeysPerProject+5 extra facet keys.
	facets := make(map[string]string)
	for i := 0; i < maxFacetKeysPerProject+5; i++ {
		facets[indexedKey(i)] = "value"
	}

	if err := store.PersistFacets(ctx, evRowID, issueRowID, facets); err != nil {
		t.Fatal(err)
	}

	keys, err := store.ListFacetKeys(ctx, store.DefaultProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) > maxFacetKeysPerProject {
		t.Fatalf("cardinality guard failed: expected at most %d keys, got %d", maxFacetKeysPerProject, len(keys))
	}
}

// helpers

func assertFacet(t *testing.T, facets map[string]string, key, want string) {
	t.Helper()
	got, ok := facets[key]
	if !ok {
		t.Errorf("missing facet key %q", key)
		return
	}
	if got != want {
		t.Errorf("facet %q: got %q, want %q", key, got, want)
	}
}

func indexedKey(i int) string {
	// Generate keys like "facet.key.aa", "facet.key.ab", ...
	return "facet.key." + string(rune('a'+i%26)) + string(rune('a'+i/26))
}

// ensure the worker package is referenced.
var _ = worker.ProcessedEvent{}
