package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// A project-bound key that is not full-scope reaches only its own project
// (#235): 404 for another project's issue, 403 for a header or project_slug that
// names another project. Full-scope keys stay cross-project.
func TestBoundKeyIsConfinedToItsProject(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()

	home, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	issue := persistTestIssue(t, store) // lives in the default project

	addKey := func(name string, projectID int64, scope string) string {
		plain := name + "-secret"
		sum := sha256.Sum256([]byte(plain))
		if _, err := store.CreateAPIKey(ctx, name, projectID, hex.EncodeToString(sum[:]), scope); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	alphaRead := addKey("alpha-read", alpha.ID, storage.APIKeyScopeRead)
	homeRead := addKey("home-read", home.ID, storage.APIKeyScopeRead)
	alphaFull := addKey("alpha-full", alpha.ID, storage.APIKeyScopeFull)

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	sessions := auth.NewSessionManager("secret", time.Hour)
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth, sessions, nil, nil)

	do := func(key, method, path string, headers map[string]string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, http.NoBody)
		req.Header.Set("x-bugbarn-api-key", key)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		server.ServeHTTP(rr, req)
		return rr
	}
	issuePath := "/api/v1/issues/" + issue.ID

	cases := []struct {
		name    string
		key     string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{"foreign issue", alphaRead, http.MethodGet, issuePath, nil, http.StatusNotFound},
		{"foreign issue events", alphaRead, http.MethodGet, issuePath + "/events", nil, http.StatusNotFound},
		{"foreign issue releases", alphaRead, http.MethodGet, issuePath + "/releases", nil, http.StatusNotFound},
		{"foreign project header", alphaRead, http.MethodGet, "/api/v1/issues", map[string]string{"X-BugBarn-Project": "default"}, http.StatusForbidden},
		{"foreign project_slug", alphaRead, http.MethodGet, "/api/v1/issues?project_slug=default", nil, http.StatusForbidden},
		{"unknown project header creates nothing", alphaRead, http.MethodGet, "/api/v1/issues", map[string]string{"X-BugBarn-Project": "brand-new"}, http.StatusForbidden},
		{"own project header", alphaRead, http.MethodGet, "/api/v1/issues", map[string]string{"X-BugBarn-Project": "alpha"}, http.StatusOK},
		{"own issue", homeRead, http.MethodGet, issuePath, nil, http.StatusOK},
		{"full-scope key stays cross-project", alphaFull, http.MethodGet, issuePath, nil, http.StatusOK},
		{"full-scope key may name another project", alphaFull, http.MethodGet, "/api/v1/issues", map[string]string{"X-BugBarn-Project": "default"}, http.StatusOK},
	}
	for _, tc := range cases {
		rr := do(tc.key, tc.method, tc.path, tc.headers)
		if rr.Code != tc.want {
			t.Errorf("%s: %s %s = %d, want %d (%s)", tc.name, tc.method, tc.path, rr.Code, tc.want, strings.TrimSpace(rr.Body.String()))
		}
	}

	if _, err := store.ProjectBySlug(ctx, "brand-new"); err == nil {
		t.Error("a confined key created a project from a header")
	}

	t.Run("sparklines leave out foreign issues", func(t *testing.T) {
		rr := do(alphaRead, http.MethodGet, "/api/v1/issues/sparklines?ids="+issue.ID, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), issue.ID) {
			t.Errorf("foreign issue in sparklines: %s", rr.Body.String())
		}
	})
}
