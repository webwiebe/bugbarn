package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

// TestServeDBBackupRequiresAdminSession verifies the full-database export is not
// reachable without an admin session once auth is enabled. Previously this
// endpoint sat in the public (pre-auth) route table and streamed the whole DB to
// any caller.
func TestServeDBBackupRequiresAdminSession(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)

	// Point the backup at a real file so the happy path can stream something.
	dbFile := filepath.Join(t.TempDir(), "bugbarn.db")
	if err := os.WriteFile(dbFile, []byte("SQLite format 3\x00fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	server.SetDBPath(dbFile)

	t.Run("unauthenticated is rejected", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/db-backup", nil)
		server.ServeHTTP(rr, req)
		// No session and no API key -> the shared auth pipeline returns 401.
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated backup: got %d want %d body=%q", rr.Code, http.StatusUnauthorized, rr.Body.String())
		}
	})

	// Log in to obtain a session cookie.
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"username":"admin","password":"change-me"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(loginRec, loginReq)
	var session *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == "bugbarn_session" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("expected session cookie, login body=%q", loginRec.Body.String())
	}

	t.Run("admin session can download", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/internal/v1/db-backup", nil)
		req.AddCookie(session)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("admin backup: got %d want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
		}
		if rr.Body.Len() == 0 {
			t.Fatal("expected non-empty backup body")
		}
	})
}
