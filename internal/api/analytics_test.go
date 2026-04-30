package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func mustOpenAnalyticsStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCollectPageView(t *testing.T) {
	t.Parallel()

	t.Run("POST valid JSON returns 202 and inserts row", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)

		body := `{"pathname":"/hello","hostname":"example.com","referrer":"https://google.com/search?q=test","sessionId":"abc123","screenWidth":1440,"duration":5000}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.collectPageView(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d body=%q", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("expected JSON response: %v", err)
		}
	})

	t.Run("POST with text/plain content-type returns 202", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)

		body := `{"pathname":"/beacon","hostname":"example.com","sessionId":"sid-plain"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect", strings.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()

		server.collectPageView(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("POST missing pathname returns 400", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)

		body := `{"hostname":"example.com","sessionId":"abc"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.collectPageView(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("OPTIONS preflight returns 204 with correct CORS headers", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)

		req := httptest.NewRequest(http.MethodOptions, "/api/v1/analytics/collect", nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rr.Code)
		}
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("expected ACAO=*, got %q", got)
		}
		if got := rr.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
			t.Errorf("expected ACAM to contain POST, got %q", got)
		}
	})

	t.Run("project resolved from query param when header absent", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)

		body := `{"pathname":"/page","hostname":"example.com","sessionId":"sess-qp"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect?project=mysite", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		server.collectPageView(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d body=%q", rr.Code, rr.Body.String())
		}

		// Verify project was created / resolved
		proj, err := store.ProjectBySlug(req.Context(), "mysite")
		if err != nil {
			t.Fatalf("expected project 'mysite' to exist: %v", err)
		}
		if proj.Slug != "mysite" {
			t.Errorf("expected slug=mysite, got %q", proj.Slug)
		}
	})
}

func TestServeAnalyticsSnippet(t *testing.T) {
	t.Parallel()

	t.Run("GET /analytics.js?project=mysite returns 200 with JS containing project slug", func(t *testing.T) {
		store := mustOpenAnalyticsStore(t)
		defer store.Close()
		server := NewServer(nil, store)
		server.SetSetupConfig("secret", "https://bugs.example.com")

		req := httptest.NewRequest(http.MethodGet, "/analytics.js?project=mysite", nil)
		rr := httptest.NewRecorder()

		server.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
		}
		ct := rr.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "application/javascript") {
			t.Errorf("expected Content-Type application/javascript, got %q", ct)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "mysite") {
			t.Errorf("expected snippet to contain 'mysite', got:\n%s", body)
		}
	})
}
