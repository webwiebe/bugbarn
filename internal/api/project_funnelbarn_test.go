package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// putFunnelBarn goes through the route table, because the bare PUT case there
// (rename) would swallow the path if the suffix case were ordered after it.
func putFunnelBarn(t *testing.T, srv *Server, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+slug+"/funnelbarn", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	if !srv.dispatchProjectItemRoutes(rr, req) {
		t.Fatal("route not handled")
	}
	return rr
}

func TestProjectFunnelBarnURLIsSetListedAndCleared(t *testing.T) {
	t.Parallel()
	srv, store := setupTestServer(t)
	const link = "https://funnelbarn.example/p/profotograaf/funnels?surface={surface}"

	rr := putFunnelBarn(t, srv, "default", `{"funnelbarn_url":"  `+link+`  "}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rr.Code, rr.Body)
	}
	p, err := store.ProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.FunnelBarnURL != link {
		t.Fatalf("stored %q, want the trimmed link", p.FunnelBarnURL)
	}

	list := httptest.NewRecorder()
	srv.listProjects(list, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	var body struct {
		Projects []struct {
			Slug          string `json:"slug"`
			FunnelBarnURL string `json:"funnelbarn_url"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pr := range body.Projects {
		if pr.Slug == "default" {
			found = true
			if pr.FunnelBarnURL != link {
				t.Fatalf("project list carries %q, want %q", pr.FunnelBarnURL, link)
			}
		}
	}
	if !found {
		t.Fatal("default project missing from the list")
	}

	if rr := putFunnelBarn(t, srv, "default", `{"funnelbarn_url":""}`); rr.Code != http.StatusOK {
		t.Fatalf("clear = %d: %s", rr.Code, rr.Body)
	}
	p, _ = store.ProjectBySlug(context.Background(), "default")
	if p.FunnelBarnURL != "" {
		t.Fatalf("link survived a clear: %q", p.FunnelBarnURL)
	}
}

func TestProjectFunnelBarnURLRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	srv, store := setupTestServer(t)
	for name, link := range map[string]string{
		"javascript scheme": "javascript:alert(1)",
		"relative":          "/funnels",
		"no host":           "https:///funnels",
		"credentials":       "https://user:pw@funnelbarn.example/",
		"not a url":         "::::",
	} {
		rr := putFunnelBarn(t, srv, "default", `{"funnelbarn_url":"`+link+`"}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rr.Code)
		}
	}
	p, _ := store.ProjectBySlug(context.Background(), "default")
	if p.FunnelBarnURL != "" {
		t.Fatalf("a rejected value was stored: %q", p.FunnelBarnURL)
	}
	if rr := putFunnelBarn(t, srv, "ghost", `{"funnelbarn_url":"https://funnelbarn.example/"}`); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project: got %d, want 404", rr.Code)
	}
}
