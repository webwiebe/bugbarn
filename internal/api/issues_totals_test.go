package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func listIssuesJSON(t *testing.T, srv *Server, url string) map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.listIssues(rr, httptest.NewRequest(http.MethodGet, url, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: status %d", url, rr.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestListIssuesReturnsTotalsPerFilter(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	var first string
	for i := range 5 {
		issue := persistTestIssueWithFingerprint(t, store, "fp-total-"+string(rune('a'+i)), "total error "+string(rune('a'+i)))
		if i == 0 {
			first = issue.ID
		}
	}

	page := listIssuesJSON(t, srv, "/api/v1/issues?limit=2")
	if got := len(page["issues"].([]any)); got != 2 {
		t.Fatalf("page size = %d, want 2", got)
	}
	if page["total"] != float64(5) || page["totalIsApproximate"] != false {
		t.Fatalf("total = %v approximate = %v, want 5 and false", page["total"], page["totalIsApproximate"])
	}
	counts := page["counts"].(map[string]any)
	if counts["open"] != float64(5) || counts["all"] != float64(5) || counts["resolved"] != float64(0) {
		t.Fatalf("counts = %v", counts)
	}

	if _, err := srv.issues.Resolve(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]float64{
		"/api/v1/issues?status=open":     4,
		"/api/v1/issues?status=resolved": 1,
		"/api/v1/issues?status=muted":    0,
		"/api/v1/issues?status=all":      5,
		"/api/v1/issues":                 5,
	} {
		resp := listIssuesJSON(t, srv, url)
		if resp["total"] != want || resp["totalIsApproximate"] != false {
			t.Errorf("%s: total = %v approximate = %v, want %v", url, resp["total"], resp["totalIsApproximate"], want)
		}
	}
}

func TestListIssuesSearchTotalIsApproximate(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	for i := range 3 {
		persistTestIssueWithFingerprint(t, store, "fp-search-"+string(rune('a'+i)), "searchable "+string(rune('a'+i)))
	}
	persistTestIssueWithFingerprint(t, store, "fp-other", "something else")

	resp := listIssuesJSON(t, srv, "/api/v1/issues?q=searchable")
	if resp["totalIsApproximate"] != true || resp["total"] != float64(3) {
		t.Fatalf("total = %v approximate = %v, want the 3 loaded issues marked approximate",
			resp["total"], resp["totalIsApproximate"])
	}
	if counts := resp["counts"].(map[string]any); counts["all"] != float64(4) {
		t.Fatalf("counts must ignore the search: %v", counts)
	}
}
