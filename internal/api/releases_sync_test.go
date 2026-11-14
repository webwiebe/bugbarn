package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Without an ingest handler, POST /api/v1/releases creates the release
// synchronously, so it is listable at once; a bad body is a 400 and other
// methods are a 405.
func TestServeReleasesRootCreatesSynchronouslyWithoutIngestHandler(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		return rr
	}

	rr := post(`{"name":"v3.0.0","environment":"production","version":"3.0.0","observedAt":"2026-04-15T12:00:00Z"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%q", rr.Code, rr.Body.String())
	}

	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/releases", nil))
	var got struct {
		Releases []struct {
			Name       string `json:"name"`
			ObservedAt string `json:"observed_at"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got.Releases) != 1 || got.Releases[0].Name != "v3.0.0" || !strings.HasPrefix(got.Releases[0].ObservedAt, "2026-04-15T12:00:00") {
		t.Fatalf("releases = %+v, want v3.0.0 observed 2026-04-15T12:00:00", got.Releases)
	}

	if rr := post(`{not json`); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, want 400", rr.Code)
	}

	del := httptest.NewRecorder()
	server.ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "/api/v1/releases", nil))
	if del.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", del.Code)
	}
}
