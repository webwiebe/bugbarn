package storage

import (
	"context"
	"path/filepath"
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
