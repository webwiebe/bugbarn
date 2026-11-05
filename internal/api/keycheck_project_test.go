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

// ingestWriteRoutes are the routes that write a project's data with an ingest
// key. Each one refuses a bound key that names another project.
var ingestWriteRoutes = []struct{ path, body string }{
	{"/api/v1/events", `{}`},
	{"/api/v1/logs", `{}`},
	{"/api/v1/alertmanager", `{}`},
	{"/api/v1/monitors/nightly-job/check-ins", `{"status":"ok"}`},
}

// A project-bound key that is not full-scope writes only to its own project on
// the ingest routes, as on the authenticated routes (#235): another project, or a
// project that does not exist, is a permanent 403 and creates nothing.
func TestBoundKeyWritesOnlyToItsOwnProject(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	ctx := context.Background()
	alpha, err := store.CreateProject(ctx, "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, "Beta", "beta"); err != nil {
		t.Fatal(err)
	}
	addKey := func(name string, projectID int64, scope string) string {
		plain := name + "-secret"
		sum := sha256.Sum256([]byte(plain))
		if _, err := store.CreateAPIKey(ctx, name, projectID, hex.EncodeToString(sum[:]), scope); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	alphaIngest := addKey("alpha-ingest", alpha.ID, storage.APIKeyScopeIngest)
	alphaFull := addKey("alpha-full", alpha.ID, storage.APIKeyScopeFull)
	const staticKey = "static-unbound-key"

	authorizer := auth.New(staticKey).WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)

	cases := []struct {
		name      string
		key       string
		project   string
		forbidden bool
	}{
		{"bound key, another project", alphaIngest, "beta", true},
		{"bound key, a project that does not exist", alphaIngest, "no-such-project", true},
		{"bound key, its own project", alphaIngest, "alpha", false},
		{"bound key, no project header", alphaIngest, "", false},
		{"full-scope key, another project", alphaFull, "beta", false},
		{"unbound key, another project", staticKey, "beta", false},
	}
	for _, rt := range ingestWriteRoutes {
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader(rt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(auth.HeaderAPIKey, tc.key)
			if tc.project != "" {
				req.Header.Set("X-BugBarn-Project", tc.project)
			}
			rr := httptest.NewRecorder()
			server.ServeHTTP(rr, req)

			switch {
			case tc.forbidden && rr.Code != http.StatusForbidden:
				t.Errorf("%s %s: got %d, want 403: %s", rt.path, tc.name, rr.Code, rr.Body.String())
			case tc.forbidden && !strings.Contains(rr.Body.String(), `"forbidden"`):
				t.Errorf("%s %s: body %q does not carry the forbidden reason", rt.path, tc.name, rr.Body.String())
			case !tc.forbidden && (rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized):
				t.Errorf("%s %s: got %d, want the key accepted: %s", rt.path, tc.name, rr.Code, rr.Body.String())
			}
		}
	}
	if _, err := store.ProjectBySlug(ctx, "no-such-project"); err == nil {
		t.Fatal("a refused write created the project it named")
	}
}
