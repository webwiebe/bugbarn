package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// GET /api/v1/releases?environment= keeps only the releases of that environment.
func TestListReleasesFiltersByEnvironment(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	for _, body := range []string{
		`{"name":"v1","environment":"production","observedAt":"2026-04-15T12:00:00Z"}`,
		`{"name":"v2","environment":"staging","observedAt":"2026-04-16T12:00:00Z"}`,
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create status = %d, body=%q", rr.Code, rr.Body.String())
		}
	}

	names := func(query string) []string {
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/releases"+query, nil))
		var got struct {
			Releases []struct {
				Name string `json:"name"`
			} `json:"releases"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := []string{}
		for _, r := range got.Releases {
			out = append(out, r.Name)
		}
		return out
	}

	if got := names(""); len(got) != 2 {
		t.Errorf("no filter = %v, want both releases", got)
	}
	if got := names("?environment=staging"); len(got) != 1 || got[0] != "v2" {
		t.Errorf("staging = %v, want [v2]", got)
	}
	if got := names("?environment=nope"); len(got) != 0 {
		t.Errorf("unknown environment = %v, want none", got)
	}
}
