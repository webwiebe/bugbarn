package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postMerge(t *testing.T, srv *Server, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+slug+"/merge", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// A merge with an environment stamps it on the source rows that name none.
func TestMergeProjectStampsEnvironment(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	ctx := context.Background()
	source, err := store.CreateProject(ctx, "Legacy", "legacy-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO releases (project_id, name, observed_at, environment) VALUES (?, 'v1', '2026-01-01T00:00:00Z', '')`, source.ID); err != nil {
		t.Fatal(err)
	}

	rr := postMerge(t, srv, "legacy-app", `{"target":"default","environment":"staging"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	def, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	var env string
	if err := store.DB().QueryRowContext(ctx,
		`SELECT environment FROM releases WHERE project_id = ? AND name = 'v1'`, def.ID).Scan(&env); err != nil {
		t.Fatal(err)
	}
	if env != "staging" {
		t.Errorf("release environment = %q, want staging", env)
	}
}

// An environment that is not a plain name is refused and nothing merges.
func TestMergeProjectRefusesBadEnvironment(t *testing.T) {
	t.Parallel()

	srv, store := setupTestServer(t)
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, "Legacy", "legacy-app"); err != nil {
		t.Fatal(err)
	}
	rr := postMerge(t, srv, "legacy-app", `{"target":"default","environment":"bad env!"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400, body %s", rr.Code, rr.Body.String())
	}
	if _, err := store.ProjectBySlug(ctx, "legacy-app"); err != nil {
		t.Errorf("source project must survive a refused merge: %v", err)
	}
}
