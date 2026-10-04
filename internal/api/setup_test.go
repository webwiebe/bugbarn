package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

// TestServeSetupGating verifies that self-service onboarding still works for new
// projects, but an already-active project's deterministic ingest key can no
// longer be fetched by an unauthenticated caller (which would allow event
// forgery into an established project's stream).
func TestServeSetupGating(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	if _, err := store.CreateProject(context.Background(), "geo", "geo"); err != nil {
		t.Fatal(err)
	}

	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)
	server.SetSetupConfig("secret", "https://bugs.example.com")

	t.Run("new project onboards without auth", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/brand-new-app", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("new project setup: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})

	t.Run("active project setup rejected without session", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/geo", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("anonymous active-project setup: got %d want %d body=%q", rr.Code, http.StatusForbidden, rr.Body.String())
		}
	})

	// Obtain an admin session.
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(loginRec, loginReq)
	var session *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "bugbarn_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("expected session cookie, body=%q", loginRec.Body.String())
	}

	t.Run("active project setup allowed with admin session", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/geo", nil)
		req.AddCookie(session)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("admin active-project setup: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
	})
}

// TestServeSetupRateLimit verifies the per-IP cap on setup-key issuance.
func TestServeSetupRateLimit(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)
	server.SetSetupConfig("secret", "https://bugs.example.com")

	var got429 bool
	for i := 0; i < setupRateLimit+5; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/setup/app-%d", i), nil)
		server.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("expected a 429 after exceeding %d setup requests", setupRateLimit)
	}
}

// TestServeSetupEndpointHost verifies the documented endpoint follows the host
// the integrator used: a bb.<domain> vanity alias prints only that alias, even
// when BUGBARN_PUBLIC_URL names the canonical host (issue #189).
func TestServeSetupEndpointHost(t *testing.T) {
	t.Parallel()

	_, trusted, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		publicURL  string
		host       string
		remoteAddr string
		headers    map[string]string
		want       string
		wantView   bool
	}{
		{name: "vanity host beats public url", publicURL: "https://bugbarn.example.com", host: "bb.customer.nl", want: "https://bb.customer.nl"},
		{name: "vanity host with port", publicURL: "https://bugbarn.example.com", host: "bb.customer.nl:443", want: "https://bb.customer.nl"},
		{name: "trusted forwarded vanity host", publicURL: "https://bugbarn.example.com", host: "bugbarn:8080", remoteAddr: "10.1.2.3:5555",
			headers: map[string]string{"X-Forwarded-Host": "bb.customer.nl"}, want: "https://bb.customer.nl"},
		{name: "untrusted forwarded host ignored", publicURL: "https://bugbarn.example.com", host: "bugbarn.example.com", remoteAddr: "203.0.113.9:5555",
			headers: map[string]string{"X-Forwarded-Host": "bb.evil.example"}, want: "https://bugbarn.example.com", wantView: true},
		{name: "canonical host uses public url", publicURL: "https://bugbarn.example.com/", host: "bugbarn-reader:8080", want: "https://bugbarn.example.com", wantView: true},
		{name: "no public url falls back to forwarded host", host: "10.43.0.7:8080", remoteAddr: "10.1.2.3:5555",
			headers: map[string]string{"X-Forwarded-Host": "errors.example.org", "X-Forwarded-Proto": "http"}, want: "http://errors.example.org", wantView: true},
		{name: "no public url falls back to request host", host: "errors.example.org", want: "https://errors.example.org", wantView: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := mustOpenStore(t)
			defer store.Close()
			server := NewServer(nil, store, nil)
			server.SetSetupConfig("secret", tc.publicURL)
			server.SetTrustedProxies([]*net.IPNet{trusted})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/customer-site", nil)
			req.Host = tc.host
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			server.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
			}
			body := rr.Body.String()

			if !strings.Contains(body, "| Endpoint  | "+tc.want+" |") {
				t.Fatalf("endpoint row missing %q:\n%s", tc.want, body)
			}
			// Every URL on the page must use the expected base, apart from the
			// GitHub links for the SDK source and the release example.
			for _, line := range strings.Split(body, "\n") {
				for _, field := range strings.Fields(line) {
					i := strings.Index(field, "://")
					if i < 0 || strings.Contains(field, "github.com/") {
						continue
					}
					if !strings.Contains(field, tc.want) {
						t.Errorf("unexpected host in %q, want only %q", field, tc.want)
					}
				}
			}
			if got := strings.Contains(body, "## View your project"); got != tc.wantView {
				t.Errorf("view section present = %v, want %v", got, tc.wantView)
			}
		})
	}
}
