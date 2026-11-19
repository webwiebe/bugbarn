package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postArchive(srv *Server, slug, action string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+slug+"/"+action, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func listSlugs(t *testing.T, srv *Server, query string) []string {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/projects"+query, nil))
	var resp struct {
		Projects []struct {
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range resp.Projects {
		out = append(out, p.Slug)
	}
	return out
}

func TestArchiveHidesAQuietProjectFromTheListAndReportsItsLastEvent(t *testing.T) {
	t.Parallel()
	srv, store := setupTestServer(t)
	ctx := context.Background()
	p, err := store.CreateProject(ctx, "Legacy", "legacy-app")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -20).Truncate(time.Second)
	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, ?, 'old', '{}')`,
		p.ID, old.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	rr := postArchive(srv, "legacy-app", "archive")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		LastEventAt string `json:"last_event_at"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.LastEventAt != old.Format(time.RFC3339) {
		t.Errorf("last_event_at = %q, want %q", resp.LastEventAt, old.Format(time.RFC3339))
	}

	if got := strings.Join(listSlugs(t, srv, ""), ","); strings.Contains(got, "legacy-app") {
		t.Errorf("archived project still listed: %s", got)
	}
	if got := strings.Join(listSlugs(t, srv, "?include_archived=true"), ","); !strings.Contains(got, "legacy-app") {
		t.Errorf("include_archived must list it: %s", got)
	}

	if rr := postArchive(srv, "legacy-app", "unarchive"); rr.Code != http.StatusOK {
		t.Fatalf("unarchive status %d", rr.Code)
	}
	if got := strings.Join(listSlugs(t, srv, ""), ","); !strings.Contains(got, "legacy-app") {
		t.Errorf("unarchived project missing: %s", got)
	}
}

func TestArchiveRefusesABusyProjectAndAnUnknownOne(t *testing.T) {
	t.Parallel()
	srv, store := setupTestServer(t)
	ctx := context.Background()
	p, _ := store.CreateProject(ctx, "Busy", "busy-app")
	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO log_entries (project_id, received_at, message, data_json) VALUES (?, ?, 'now', '{}')`,
		p.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if rr := postArchive(srv, "busy-app", "archive"); rr.Code != http.StatusConflict {
		t.Errorf("busy: status %d, want 409 (%s)", rr.Code, rr.Body.String())
	}
	if rr := postArchive(srv, "no-such-app", "archive"); rr.Code != http.StatusNotFound {
		t.Errorf("unknown: status %d, want 404", rr.Code)
	}
	if rr := postArchive(srv, "default", "unarchive"); rr.Code != http.StatusNotFound {
		t.Errorf("unarchive of an active project: status %d, want 404", rr.Code)
	}
}
