package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// newOIDCServer builds a Server wired to a fake IdP: real OIDC client, real
// SQLite session store, admin user enabled so the auth gate engages.
func newOIDCServer(t *testing.T) (*Server, *oidctest.IdP, *storage.Store) {
	t.Helper()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	idp, err := oidctest.New("bugbarn-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)

	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager("test-secret", 12*time.Hour)
	server := NewServerWithAuth(nil, store, userAuth, sessions, nil, nil)
	server.SetOIDCClient(auth.NewOIDCClient(auth.OIDCConfig{
		Issuer:        idp.Issuer(),
		ClientID:      "bugbarn-test",
		ClientSecret:  "sek",
		RedirectURL:   "http://bugbarn.example.com/api/v1/oidc/callback",
		RequiredGroup: "bugbarn-users",
	}))
	return server, idp, store
}

// insertSessionCookie persists a session row and returns the matching cookie.
func insertSessionCookie(t *testing.T, store *storage.Store, ws storage.WebSession) *http.Cookie {
	t.Helper()
	handle := auth.NewSessionHandle()
	ws.IDHash = auth.HashSessionHandle(handle)
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = time.Now().UTC()
	}
	if ws.AbsoluteExpiresAt.IsZero() {
		ws.AbsoluteExpiresAt = time.Now().UTC().Add(12 * time.Hour)
	}
	if err := store.InsertWebSession(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "bugbarn_session", Value: handle}
}

func getMe(server *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	server.ServeHTTP(rr, req)
	return rr
}

func TestSessionMiddlewareRefreshOnExpiry(t *testing.T) {
	server, idp, store := newOIDCServer(t)
	now := time.Now().UTC()
	cookie := insertSessionCookie(t, store, storage.WebSession{
		Username:        "alice",
		AuthMethod:      storage.WebSessionAuthOIDC,
		IdpSub:          "sub-1",
		IdpSid:          "sid-1",
		RefreshToken:    "rt-1",
		AccessToken:     "at-1",
		AccessExpiresAt: now.Add(-time.Minute),
	})
	freshID := idp.SignJWT(idp.IDTokenClaims("sub-1", "sid-1", "", []string{"bugbarn-users"}))
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("grant_type") != "refresh_token" || r.PostFormValue("refresh_token") != "rt-1" {
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		oidctest.WriteTokenResponse(w, freshID, "at-2", "rt-2", 900)
	})

	rr := getMe(server, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("me after refresh = %d body=%q", rr.Code, rr.Body.String())
	}
	// New claims must be visible server-side: tokens rotated + snapshot updated.
	ws, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	if ws.RefreshToken != "rt-2" || ws.AccessToken != "at-2" {
		t.Errorf("tokens not rotated: %+v", ws)
	}
	if ws.AccessExpiresAt.Before(time.Now().Add(10 * time.Minute)) {
		t.Errorf("access expiry not extended: %v", ws.AccessExpiresAt)
	}
}

func TestSessionMiddlewareInvalidGrant401(t *testing.T) {
	server, idp, store := newOIDCServer(t)
	now := time.Now().UTC()
	cookie := insertSessionCookie(t, store, storage.WebSession{
		Username: "alice", AuthMethod: storage.WebSessionAuthOIDC,
		IdpSub: "sub-1", RefreshToken: "rt-dead",
		AccessExpiresAt: now.Add(-time.Minute),
	})
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_grant")
	})

	if rr := getMe(server, cookie); rr.Code != http.StatusUnauthorized {
		t.Fatalf("me after invalid_grant = %d", rr.Code)
	}
	// invalid_grant kills the row immediately — no grace.
	if _, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(cookie.Value)); err == nil {
		t.Error("session row must be deleted on invalid_grant")
	}
}

func TestSessionMiddlewareOutageGrace(t *testing.T) {
	server, idp, store := newOIDCServer(t)
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		oidctest.WriteTokenError(w, http.StatusInternalServerError, "server_error")
	})
	now := time.Now().UTC()

	t.Run("stale served within grace", func(t *testing.T) {
		cookie := insertSessionCookie(t, store, storage.WebSession{
			Username: "alice", AuthMethod: storage.WebSessionAuthOIDC,
			IdpSub: "sub-1", RefreshToken: "rt-1",
			AccessExpiresAt: now.Add(-time.Minute),
		})
		if rr := getMe(server, cookie); rr.Code != http.StatusOK {
			t.Fatalf("stale within grace = %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("401 past the grace ceiling", func(t *testing.T) {
		cookie := insertSessionCookie(t, store, storage.WebSession{
			Username: "bob", AuthMethod: storage.WebSessionAuthOIDC,
			IdpSub: "sub-2", IdpSid: "sid-2", RefreshToken: "rt-2",
			AccessExpiresAt:     now.Add(-3 * time.Hour),
			RefreshFailingSince: now.Add(-2 * time.Hour), // default grace is 1h
		})
		if rr := getMe(server, cookie); rr.Code != http.StatusUnauthorized {
			t.Fatalf("past grace = %d", rr.Code)
		}
	})

	t.Run("custom grace ceiling honored", func(t *testing.T) {
		server.SetOIDCRefreshGrace(3 * time.Hour)
		defer server.SetOIDCRefreshGrace(time.Hour)
		cookie := insertSessionCookie(t, store, storage.WebSession{
			Username: "carol", AuthMethod: storage.WebSessionAuthOIDC,
			IdpSub: "sub-3", IdpSid: "sid-3", RefreshToken: "rt-3",
			AccessExpiresAt:     now.Add(-3 * time.Hour),
			RefreshFailingSince: now.Add(-2 * time.Hour),
		})
		if rr := getMe(server, cookie); rr.Code != http.StatusOK {
			t.Fatalf("within widened grace = %d", rr.Code)
		}
	})
}

func TestSessionMiddlewareAbsoluteCap(t *testing.T) {
	server, _, store := newOIDCServer(t)
	now := time.Now().UTC()
	cookie := insertSessionCookie(t, store, storage.WebSession{
		Username: "alice", AuthMethod: storage.WebSessionAuthLocal,
		CreatedAt:         now.Add(-13 * time.Hour),
		AbsoluteExpiresAt: now.Add(-time.Hour),
	})
	if rr := getMe(server, cookie); rr.Code != http.StatusUnauthorized {
		t.Fatalf("past absolute cap = %d", rr.Code)
	}
	if _, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(cookie.Value)); err == nil {
		t.Error("expired session row must be deleted")
	}
}

func TestAuthGateEngagesForOIDCOnly(t *testing.T) {
	// OIDC configured but NO local users: the pipeline must still require
	// auth (the old gate keyed off local users alone and failed open).
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	sessions := auth.NewSessionManager("test-secret", time.Hour)
	server := NewServerWithAuth(nil, store, nil, sessions, nil, nil)
	server.SetOIDCClient(auth.NewOIDCClient(auth.OIDCConfig{
		Issuer: "https://iam.example.com", ClientID: "c", ClientSecret: "s",
		RedirectURL: "https://bugbarn.example.com/api/v1/oidc/callback",
	}))

	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("protected endpoint without session = %d, want 401", rr.Code)
	}

	// Local login must refuse (no local credentials exist), not mint a session.
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
	server.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusUnauthorized {
		t.Fatalf("local login in OIDC-only mode = %d, want 401", loginRR.Code)
	}

	// /me must report authEnabled=true and reject anonymous callers.
	if rr := getMe(server, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("me without session = %d, want 401", rr.Code)
	}
}
