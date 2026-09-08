package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// The facets endpoints are what the dashboard's environment switcher is built
// from, and the issue list's facet filter is what it drives. Both read the
// issue_facets projection, so exercise them through the full stack.
func TestFacetsEndpointsAndIssueFilter(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	ctx := storage.WithProjectID(context.Background(), store.DefaultProjectID())

	// Fingerprint the events the way the pipeline does. A hand-written
	// fingerprint would be rewritten under the test by the store's own
	// fingerprint migration, splitting one issue in two.
	persist := func(environment string, n int) storage.Issue {
		t.Helper()
		var issue storage.Issue
		for i := 0; i < n; i++ {
			evt := event.Event{
				ObservedAt: time.Now().UTC(),
				ReceivedAt: time.Now().UTC().Add(time.Second),
				Severity:   "ERROR",
				Message:    "facet endpoint test error",
				// Unique per event, exactly like a real trace id.
				TraceID:    environment + "-trace-" + strconv.Itoa(i),
				Exception:  event.Exception{Type: "FacetEndpointError", Message: "facet endpoint test error"},
				Attributes: map[string]any{"environment": environment},
			}
			snapshot := fingerprint.SnapshotFor(evt)
			evt.Fingerprint = fingerprint.Fingerprint(evt)
			evt.FingerprintMaterial = snapshot.Material
			evt.FingerprintExplanation = snapshot.Explanation

			var err error
			issue, _, _, _, err = store.PersistProcessedEvent(ctx, worker.ProcessedEvent{
				Event:                  evt,
				Fingerprint:            evt.Fingerprint,
				FingerprintMaterial:    snapshot.Material,
				FingerprintExplanation: snapshot.Explanation,
			})
			if err != nil {
				t.Fatalf("persist: %v", err)
			}
		}
		return issue
	}

	prodIssue := persist("production", 3)
	stagingIssue := persist("staging", 1)
	if prodIssue.ID == stagingIssue.ID {
		t.Fatalf("both environments landed on issue %s; the test needs two issues", prodIssue.ID)
	}

	// GET /api/v1/facets
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/facets", nil).WithContext(ctx)
	srv.serveFacetsRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list keys: status %d, body %s", rr.Code, rr.Body.String())
	}
	var keysResp struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &keysResp); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]bool, len(keysResp.Keys))
	for _, k := range keysResp.Keys {
		keys[k] = true
	}
	if !keys["attributes.environment"] {
		t.Errorf("facet keys %v do not include attributes.environment, which the environment switcher reads", keysResp.Keys)
	}
	for _, unwanted := range []string{"traceId", "message"} {
		if keys[unwanted] {
			t.Errorf("facet keys include %q; per-event keys are not projected", unwanted)
		}
	}

	// GET /api/v1/facets/attributes.environment
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/facets/attributes.environment", nil).WithContext(ctx)
	srv.serveFacetsRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list values: status %d, body %s", rr.Code, rr.Body.String())
	}
	var valuesResp struct {
		Key    string   `json:"key"`
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &valuesResp); err != nil {
		t.Fatal(err)
	}
	if len(valuesResp.Values) != 2 || valuesResp.Values[0] != "production" || valuesResp.Values[1] != "staging" {
		t.Errorf("environment values = %v, want [production staging]", valuesResp.Values)
	}

	// GET /api/v1/issues?attributes.environment=production
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/issues?attributes.environment=production", nil).WithContext(ctx)
	srv.listIssues(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list issues: status %d, body %s", rr.Code, rr.Body.String())
	}
	var issuesResp struct {
		Issues []domain.Issue `json:"issues"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &issuesResp); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, issue := range issuesResp.Issues {
		seen[issue.ID]++
	}
	if seen[prodIssue.ID] == 0 {
		t.Errorf("filtering on production dropped %s, the issue that carries it", prodIssue.ID)
	}
	if seen[stagingIssue.ID] != 0 {
		t.Errorf("filtering on production returned %s, which was only ever seen on staging", stagingIssue.ID)
	}
	// Three events, one row in the projection, one row in the join result: the
	// filter must not multiply an issue by its event count.
	if seen[prodIssue.ID] > 1 {
		t.Errorf("issue %s came back %d times; the facet join is multiplying rows", prodIssue.ID, seen[prodIssue.ID])
	}
}
