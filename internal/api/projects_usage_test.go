package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// decodeProjectsResponse pulls the decoded body of a GET /api/v1/projects call.
func decodeProjectsResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rr.Body.String())
	}
	return payload
}

func firstProject(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	list, ok := payload["projects"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("no projects in payload: %v", payload)
	}
	p, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("project is not an object: %v", list[0])
	}
	return p
}

// The default list is the one every dashboard load blocks on. It must not pay
// for the usage aggregate, so the counts are absent unless asked for.
func TestListProjectsOmitsUsageByDefault(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	server.listProjects(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))

	payload := decodeProjectsResponse(t, rr)
	if _, present := payload["usage_available"]; present {
		t.Error("usage_available reported without ?usage=true")
	}
	p := firstProject(t, payload)
	for _, key := range []string{"issue_count", "event_count", "log_count"} {
		if _, present := p[key]; present {
			t.Errorf("%s present without ?usage=true — the expensive aggregate ran anyway", key)
		}
	}
	// The list itself must still be complete.
	for _, key := range []string{"id", "name", "slug", "status"} {
		if _, present := p[key]; !present {
			t.Errorf("%s missing from the project list", key)
		}
	}
}

func TestListProjectsIncludesUsageWhenRequested(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	server.listProjects(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects?usage=true", nil))

	payload := decodeProjectsResponse(t, rr)
	if available, _ := payload["usage_available"].(bool); !available {
		t.Fatalf("usage_available = %v, want true", payload["usage_available"])
	}
	p := firstProject(t, payload)
	for _, key := range []string{"issue_count", "event_count", "log_count"} {
		if _, present := p[key]; !present {
			t.Errorf("%s missing from a ?usage=true response", key)
		}
	}
}

// A genuine zero must still be reported as 0, not elided — omitempty on a
// pointer field only drops nil, and this pins that.
func TestListProjectsReportsGenuineZeroCounts(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	rr := httptest.NewRecorder()
	server.listProjects(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects?usage=true", nil))

	p := firstProject(t, decodeProjectsResponse(t, rr))
	count, present := p["event_count"]
	if !present {
		t.Fatal("event_count elided for a project with no events; 0 is a real answer")
	}
	if n, _ := count.(float64); n != 0 {
		t.Errorf("event_count = %v, want 0", n)
	}
}

// The regression this whole change exists to prevent: a failing usage query
// used to be discarded, so every project rendered as 0 issues / 0 events /
// 0 logs and the caller could not tell the difference.
func TestListProjectsReportsUsageFailureInsteadOfZeros(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	// Break only the usage aggregate. Dropping the events table leaves the
	// project list perfectly healthy while the three-way count join fails, which
	// is exactly the shape of the production failure: the list succeeded and the
	// counts did not.
	if _, err := store.DB().ExecContext(context.Background(), `DROP TABLE events`); err != nil {
		t.Fatalf("drop events: %v", err)
	}

	rr := httptest.NewRecorder()
	server.listProjects(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects?usage=true", nil))

	payload := decodeProjectsResponse(t, rr)
	available, present := payload["usage_available"]
	if !present {
		t.Fatal("usage_available missing; the caller cannot tell the counts are unavailable")
	}
	if ok, _ := available.(bool); ok {
		t.Error("usage_available = true after the usage query failed")
	}

	p := firstProject(t, payload)
	for _, key := range []string{"issue_count", "event_count", "log_count"} {
		if v, ok := p[key]; ok {
			t.Errorf("%s = %v present after a usage failure; absent means \"unknown\", 0 would be a lie", key, v)
		}
	}
	// The projects themselves must still come back — failing the whole request
	// would hide the list along with the counts.
	if p["slug"] == nil || p["slug"] == "" {
		t.Error("project list lost its contents when usage failed")
	}
}
