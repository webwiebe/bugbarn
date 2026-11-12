package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func silenceWatch(t *testing.T, srv *Server, method, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/v1/projects/"+slug+"/silence-watch", reader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.projectSilenceWatch(rr, req)
	return rr
}

func silenceWatchValue(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		SilenceWatch string `json:"silence_watch"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	return out.SilenceWatch
}

func TestSilenceWatchOptsAProjectOutAndBackIn(t *testing.T) {
	t.Parallel()
	srv, store := setupTestServer(t)

	if rr := silenceWatch(t, srv, http.MethodGet, "default", ""); rr.Code != http.StatusOK || silenceWatchValue(t, rr) != "" {
		t.Fatalf("new project: status %d body %s, want watched", rr.Code, rr.Body.String())
	}
	if rr := silenceWatch(t, srv, http.MethodPut, "default", `{"silence_watch":"off"}`); rr.Code != http.StatusOK {
		t.Fatalf("PUT off: status %d body %s", rr.Code, rr.Body.String())
	}
	if got, err := store.Domains().Projects.ProjectSilenceWatch(context.Background(), "default"); err != nil || got != "off" {
		t.Fatalf("stored = %q err %v, want off", got, err)
	}
	if rr := silenceWatch(t, srv, http.MethodGet, "default", ""); silenceWatchValue(t, rr) != "off" {
		t.Fatalf("GET after PUT: %s", rr.Body.String())
	}
	if rr := silenceWatch(t, srv, http.MethodPut, "default", `{"silence_watch":""}`); rr.Code != http.StatusOK {
		t.Fatalf("PUT empty: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := silenceWatch(t, srv, http.MethodGet, "default", ""); silenceWatchValue(t, rr) != "" {
		t.Fatalf("GET after clearing: %s", rr.Body.String())
	}
}

func TestSilenceWatchRejectsBadInputAndUnknownProjects(t *testing.T) {
	t.Parallel()
	srv, _ := setupTestServer(t)

	cases := []struct {
		name, method, slug, body string
		want                     int
	}{
		{"unknown value", http.MethodPut, "default", `{"silence_watch":"sometimes"}`, http.StatusBadRequest},
		{"malformed json", http.MethodPut, "default", `{`, http.StatusBadRequest},
		{"unknown project on PUT", http.MethodPut, "no-such-project", `{"silence_watch":"off"}`, http.StatusNotFound},
		{"unknown project on GET", http.MethodGet, "no-such-project", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rr := silenceWatch(t, srv, tc.method, tc.slug, tc.body); rr.Code != tc.want {
				t.Errorf("status %d, want %d (body %s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}
