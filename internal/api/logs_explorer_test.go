package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func getLogs(t *testing.T, server *Server, path string, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path+"?"+params.Encode(), nil)
	req = req.WithContext(domain.WithProjectID(req.Context(), 1))
	switch path {
	case "/api/v1/logs/histogram":
		server.serveLogsHistogram(rr, req)
	case "/api/v1/logs/facets":
		server.serveLogsFacets(rr, req)
	default:
		server.serveLogs(rr, req)
	}
	return rr
}

func TestLogExplorerEndpoints(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	body := `{"logs":[{"level":"error","message":"explorer-hit","user":"u1"},{"level":"info","message":"explorer-other","user":"u2"}]}`
	if rr := postLogs(t, server, "application/json", body); rr.Code != http.StatusNoContent {
		t.Fatalf("ingest: got %d body=%q", rr.Code, rr.Body.String())
	}

	now := time.Now().UTC()
	window := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339Nano)},
		"to":   {now.Add(time.Minute).Format(time.RFC3339Nano)},
	}

	t.Run("attr filter narrows the list", func(t *testing.T) {
		p := url.Values{"from": window["from"], "to": window["to"], "attr": {"user=u1"}}
		rr := getLogs(t, server, "/api/v1/logs", p)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "explorer-hit") || strings.Contains(rr.Body.String(), "explorer-other") {
			t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("histogram", func(t *testing.T) {
		rr := getLogs(t, server, "/api/v1/logs/histogram", window)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"buckets"`) {
			t.Fatalf("got %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("facet keys and values", func(t *testing.T) {
		rr := getLogs(t, server, "/api/v1/logs/facets", window)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "user") {
			t.Fatalf("keys: got %d body=%q", rr.Code, rr.Body.String())
		}
		p := url.Values{"from": window["from"], "to": window["to"], "key": {"user"}}
		rr = getLogs(t, server, "/api/v1/logs/facets", p)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "u1") {
			t.Fatalf("values: got %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("bad input is 400", func(t *testing.T) {
		for _, p := range []url.Values{
			{"from": {"yesterday"}},
			{"attr": {"novalue"}},
		} {
			for _, path := range []string{"/api/v1/logs", "/api/v1/logs/histogram", "/api/v1/logs/facets"} {
				if rr := getLogs(t, server, path, p); rr.Code != http.StatusBadRequest {
					t.Errorf("%s %v: got %d want 400", path, p, rr.Code)
				}
			}
		}
	})

	t.Run("other project sees nothing", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/logs?"+window.Encode(), nil)
		req = req.WithContext(domain.WithProjectID(req.Context(), 999))
		server.serveLogs(rr, req)
		if strings.Contains(rr.Body.String(), "explorer-") {
			t.Fatalf("project 999 saw project 1 logs: %q", rr.Body.String())
		}
	})
}
