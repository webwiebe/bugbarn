package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A marker whose X-BugBarn-Project header did not resolve to the request's
// project answers a retryable 503 and stores nothing, so it cannot land in the
// default project the scope middleware falls back to.
func TestReleaseMarkerFailsWhenItsProjectDidNotResolve(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(nil, store, nil)

	post := func(projectID int64, project string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(`{"name":"v1","environment":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		if project != "" {
			req.Header.Set("X-BugBarn-Project", project)
		}
		rr := httptest.NewRecorder()
		server.serveReleasesRoot(rr, req.WithContext(domain.WithProjectID(ctx, projectID)))
		return rr
	}
	stored := func() int {
		t.Helper()
		n := 0
		for _, id := range []int64{store.DefaultProjectID(), alpha.ID} {
			releases, err := store.ListReleases(domain.WithProjectID(ctx, id))
			if err != nil {
				t.Fatal(err)
			}
			n += len(releases)
		}
		return n
	}

	// The lookup failed and the middleware fell back to the default project.
	for _, tc := range []struct {
		name    string
		id      int64
		project string
	}{
		{"fell back to the default project", store.DefaultProjectID(), "alpha"},
		{"no project at all", 0, "alpha"},
		{"named project does not exist", store.DefaultProjectID(), "no-such-project"},
	} {
		rr := post(tc.id, tc.project)
		if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: got %d retry-after=%q, want a retryable 503", tc.name, rr.Code, rr.Header().Get("Retry-After"))
		}
	}
	if n := stored(); n != 0 {
		t.Fatalf("the refused markers stored %d releases", n)
	}

	if rr := post(alpha.ID, "alpha"); rr.Code >= 300 {
		t.Fatalf("resolved project: got %d: %s", rr.Code, rr.Body.String())
	}
	// No header keeps the existing default for a shared key.
	if rr := post(store.DefaultProjectID(), ""); rr.Code >= 300 {
		t.Fatalf("no header: got %d: %s", rr.Code, rr.Body.String())
	}
	if n := stored(); n != 2 {
		t.Fatalf("stored %d releases, want 2", n)
	}
}

// A failed project lookup is retryable: the marker is refused with a 503.
func TestReleaseMarkerRetryableWhenTheProjectLookupFails(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	server := NewServer(nil, store, nil)
	id := store.DefaultProjectID()
	store.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(`{"name":"v1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Project", "alpha")
	rr := httptest.NewRecorder()
	server.serveReleasesRoot(rr, req.WithContext(domain.WithProjectID(context.Background(), id)))
	if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d retry-after=%q, want a retryable 503", rr.Code, rr.Header().Get("Retry-After"))
	}
}
