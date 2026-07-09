package api

import (
	"context"
	"fmt"
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
