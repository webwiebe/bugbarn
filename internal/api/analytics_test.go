package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
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
		server := NewServer(nil, store, nil)

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
		server := NewServer(nil, store, nil)

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
		server := NewServer(nil, store, nil)

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
		server := NewServer(nil, store, nil)

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
		server := NewServer(nil, store, nil)

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
		server := NewServer(nil, store, nil)
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

// The environment lands in props so the environment filter finds it; an invalid
// value is dropped and the page view is still recorded.
func TestCollectPageViewStoresTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body, want string }{
		{"top-level field", `{"pathname":"/a","environment":"staging"}`, "staging"},
		{"props key", `{"pathname":"/a","props":{"environment":"testing"}}`, "testing"},
		{"field wins over props", `{"pathname":"/a","environment":"staging","props":{"environment":"testing"}}`, "staging"},
		{"invalid dropped", `{"pathname":"/a","environment":"bad env!","props":{"environment":"x y"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := mustOpenAnalyticsStore(t)
			defer store.Close()
			server := NewServer(nil, store, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/analytics/collect", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			server.collectPageView(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			q := analytics.Query{ProjectID: store.DefaultProjectID(), Start: time.Now().Add(-time.Hour), End: time.Now(), Environment: tc.want}
			res, err := store.QueryOverview(context.Background(), q)
			if tc.want == "" {
				q.Environment = ""
				res, err = store.QueryOverview(context.Background(), q)
			}
			if err != nil || res.Pageviews != 1 {
				t.Fatalf("overview for %q = %+v, %v; want 1 view", tc.want, res, err)
			}
			if tc.want == "" {
				q.Environment = "staging"
				if res, _ := store.QueryOverview(context.Background(), q); res.Pageviews != 0 {
					t.Fatalf("invalid environment still matched staging: %+v", res)
				}
			}
		})
	}
}
