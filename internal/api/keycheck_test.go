package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
)

func keyCheckServer(t *testing.T, lookup func(context.Context, string) (int64, string, bool, error)) *Server {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	a := auth.New("").WithDBLookup(lookup, nil)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	return NewServerWithAuth(ingest.NewHandler(a, nil, 1<<20), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)
}

// Every route that checks an API key must answer a failed lookup with a
// retryable 503, and a key the store does not know with 401 (bugbarn#234).
func TestKeyLookupErrorIsRetryableOnEveryKeyedRoute(t *testing.T) {
	t.Parallel()
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/events"},
		{http.MethodPost, "/api/v1/logs"},
		{http.MethodPost, "/api/v1/alertmanager"},
		{http.MethodPost, "/api/v1/telemetry/security"},
		{http.MethodPost, "/api/v1/telemetry/metrics"},
		{http.MethodGet, "/api/v1/issues"},
		{http.MethodGet, "/api/v1/projects"},
	}
	failing := keyCheckServer(t, func(context.Context, string) (int64, string, bool, error) {
		return 0, "", false, errors.New("database is locked")
	})
	unknown := keyCheckServer(t, func(context.Context, string) (int64, string, bool, error) {
		return 0, "", false, nil
	})
	for _, rt := range routes {
		rt := rt
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name string
				srv  *Server
				want int
			}{
				{"lookup error", failing, http.StatusServiceUnavailable},
				{"unknown key", unknown, http.StatusUnauthorized},
			} {
				req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
				req.Header.Set("X-BugBarn-API-Key", "some-key")
				rr := httptest.NewRecorder()
				tc.srv.ServeHTTP(rr, req)
				if rr.Code != tc.want {
					t.Errorf("%s: got %d, want %d: %s", tc.name, rr.Code, tc.want, rr.Body.String())
				}
			}
		})
	}
}

// A store that answers the first key check and fails the next: the project
// resolution after authentication must not turn that into a 400 or an
// unbound request, it answers 503 as well (bugbarn#234).
func TestKeyLookupFailureAfterAuthenticationIsRetryable(t *testing.T) {
	t.Parallel()
	for _, rt := range []struct{ name, method, path string }{
		{"logs ingest", http.MethodPost, "/api/v1/logs"},
		{"monitor check-in", http.MethodPost, "/api/v1/monitors/nightly/check-ins"},
	} {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := keyCheckServer(t, func(context.Context, string) (int64, string, bool, error) {
				if calls.Add(1) == 1 {
					return 1, "full", true, nil
				}
				return 0, "", false, errors.New("database is locked")
			})
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{"status":"ok"}`))
			req.Header.Set("X-BugBarn-API-Key", "some-key")
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("got %d, want 503: %s", rr.Code, rr.Body.String())
			}
		})
	}
}
