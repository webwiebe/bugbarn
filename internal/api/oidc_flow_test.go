package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// startOIDCLogin drives GET /api/v1/oidc/login and returns the authorize URL
// plus the short-lived cookies the callback needs.
func startOIDCLogin(t *testing.T, server *Server, query string) (*url.URL, []*http.Cookie) {
	t.Helper()
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/oidc/login"+query, nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("oidc login = %d", rr.Code)
	}
	authorize, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return authorize, rr.Result().Cookies()
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestOIDCLoginCallbackFlow(t *testing.T) {
	server, idp, store := newOIDCServer(t)

	authorize, cookies := startOIDCLogin(t, server, "?return_to=%2Fapp%2F%23%2Faccount")
	q := authorize.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("authorize URL missing PKCE: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "offline_access") {
		t.Errorf("scope missing offline_access: %q", q.Get("scope"))
	}
	state, verifier := splitStateCookie(cookieValue(cookies, oidcStateCookie))
	if state != q.Get("state") || verifier == "" {
		t.Fatalf("state cookie must carry state+verifier, got state=%q verifier=%q", state, verifier)
	}
	nonce := cookieValue(cookies, oidcNonceCookie)

	// Script the code exchange: verifier must round-trip, tokens come back.
	idToken := idp.SignJWT(idp.IDTokenClaims("sub-1", "sid-1", nonce, []string{"bugbarn-users"}))
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("code") != "code-1" || r.PostFormValue("code_verifier") != verifier {
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		oidctest.WriteTokenResponse(w, idToken, "at-1", "rt-1", 900)
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/oidc/callback?code=code-1&state="+url.QueryEscape(state), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("callback = %d body=%q", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/app/#/account" {
		t.Errorf("return_to redirect = %q", loc)
	}

	session := cookieValue(rr.Result().Cookies(), "bugbarn_session")
	if session == "" {
		t.Fatal("expected session cookie")
	}
	ws, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(session))
	if err != nil {
		t.Fatal(err)
	}
	if ws.AuthMethod != storage.WebSessionAuthOIDC || ws.IdpSub != "sub-1" || ws.IdpSid != "sid-1" {
		t.Errorf("row identity = %+v", ws)
	}
	if ws.RefreshToken != "rt-1" || ws.AccessToken != "at-1" || ws.IDToken != idToken {
		t.Errorf("row tokens = %+v", ws)
	}
	if !strings.Contains(ws.ClaimsJSON, "bugbarn-users") {
		t.Errorf("claims snapshot = %q", ws.ClaimsJSON)
	}
	if ws.AbsoluteExpiresAt.Before(time.Now().Add(11 * time.Hour)) {
		t.Errorf("absolute cap = %v, want ~12h", ws.AbsoluteExpiresAt)
	}

	// The fresh session authenticates.
	if rr := getMe(server, &http.Cookie{Name: "bugbarn_session", Value: session}); rr.Code != http.StatusOK {
		t.Fatalf("me with new session = %d", rr.Code)
	}
}

func TestOIDCCallbackStateMismatch(t *testing.T) {
	server, _, _ := newOIDCServer(t)
	_, cookies := startOIDCLogin(t, server, "")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/oidc/callback?code=c&state=attacker-state", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("state mismatch = %d", rr.Code)
	}
}

func TestBackchannelLogout(t *testing.T) {
	server, idp, store := newOIDCServer(t)
	now := time.Now().UTC()

	postLogoutToken := func(token string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		form := url.Values{"logout_token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/oidc/backchannel-logout",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		server.ServeHTTP(rr, req)
		return rr
	}

	t.Run("kills sessions by sid", func(t *testing.T) {
		cookie := insertSessionCookie(t, store, storage.WebSession{
			Username: "alice", AuthMethod: storage.WebSessionAuthOIDC,
			IdpSub: "sub-1", IdpSid: "sid-1", RefreshToken: "rt-1",
			AccessExpiresAt: now.Add(15 * time.Minute),
		})
		if rr := postLogoutToken(idp.LogoutToken("sub-1", "sid-1", nil)); rr.Code != http.StatusOK {
			t.Fatalf("backchannel = %d body=%q", rr.Code, rr.Body.String())
		}
		if rr := getMe(server, cookie); rr.Code != http.StatusUnauthorized {
			t.Fatalf("session must be dead after backchannel logout, me = %d", rr.Code)
		}
	})

	t.Run("falls back to sub", func(t *testing.T) {
		cookie := insertSessionCookie(t, store, storage.WebSession{
			Username: "bob", AuthMethod: storage.WebSessionAuthOIDC,
			IdpSub: "sub-2", IdpSid: "sid-2", RefreshToken: "rt-2",
			AccessExpiresAt: now.Add(15 * time.Minute),
		})
		if rr := postLogoutToken(idp.LogoutToken("sub-2", "", nil)); rr.Code != http.StatusOK {
			t.Fatalf("backchannel by sub = %d", rr.Code)
		}
		if rr := getMe(server, cookie); rr.Code != http.StatusUnauthorized {
			t.Fatalf("me after sub logout = %d", rr.Code)
		}
	})

	t.Run("invalid tokens get 400", func(t *testing.T) {
		for name, token := range map[string]string{
			"garbage":       "not-a-jwt",
			"with nonce":    idp.LogoutToken("sub-1", "sid-1", func(c map[string]any) { c["nonce"] = "n" }),
			"no events":     idp.LogoutToken("sub-1", "sid-1", func(c map[string]any) { delete(c, "events") }),
			"stale iat":     idp.LogoutToken("sub-1", "sid-1", func(c map[string]any) { c["iat"] = time.Now().Add(-time.Hour).Unix() }),
			"no sub no sid": idp.LogoutToken("", "", nil),
		} {
			if rr := postLogoutToken(token); rr.Code != http.StatusBadRequest {
				t.Errorf("%s: got %d, want 400", name, rr.Code)
			}
		}
	})

	t.Run("missing token gets 400", func(t *testing.T) {
		if rr := postLogoutToken(""); rr.Code != http.StatusBadRequest {
			t.Errorf("empty = %d", rr.Code)
		}
	})
}

func TestServerDrivenLogout(t *testing.T) {
	server, idp, store := newOIDCServer(t)
	now := time.Now().UTC()
	cookie := insertSessionCookie(t, store, storage.WebSession{
		Username: "alice", AuthMethod: storage.WebSessionAuthOIDC,
		IdpSub: "sub-1", IdpSid: "sid-1",
		IDToken: "idt-1", RefreshToken: "rt-1",
		AccessExpiresAt: now.Add(15 * time.Minute),
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logout", nil)
	req.AddCookie(cookie)
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout = %d", rr.Code)
	}

	var payload struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	logoutURL, err := url.Parse(payload.LogoutURL)
	if err != nil || logoutURL.Query().Get("id_token_hint") != "idt-1" {
		t.Errorf("logout_url = %q (%v)", payload.LogoutURL, err)
	}
	if !strings.Contains(logoutURL.Query().Get("post_logout_redirect_uri"), "/api/v1/oidc/logged-out") {
		t.Errorf("post_logout_redirect_uri = %q", logoutURL.Query().Get("post_logout_redirect_uri"))
	}

	// The refresh token family died at the IdP, not just locally.
	if revoked := idp.Revoked(); len(revoked) != 1 || revoked[0] != "rt-1" {
		t.Errorf("revoked = %v", revoked)
	}
	// And the local row is gone.
	if _, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(cookie.Value)); err == nil {
		t.Error("session row must be deleted on logout")
	}
	// Local sessions produce no logout_url.
	localCookie := insertSessionCookie(t, store, storage.WebSession{
		Username: "admin", AuthMethod: storage.WebSessionAuthLocal,
	})
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/logout", nil)
	req2.AddCookie(localCookie)
	server.ServeHTTP(rr2, req2)
	var payload2 struct {
		LogoutURL string `json:"logout_url"`
	}
	_ = json.Unmarshal(rr2.Body.Bytes(), &payload2)
	if payload2.LogoutURL != "" {
		t.Errorf("local logout_url = %q, want empty", payload2.LogoutURL)
	}
}

func TestOIDCLoggedOutClearsRow(t *testing.T) {
	server, _, store := newOIDCServer(t)
	cookie := insertSessionCookie(t, store, storage.WebSession{
		Username: "alice", AuthMethod: storage.WebSessionAuthOIDC,
		IdpSub: "sub-1", AccessExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/oidc/logged-out", nil)
	req.AddCookie(cookie)
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("logged-out = %d", rr.Code)
	}
	if _, err := store.GetWebSession(t.Context(), auth.HashSessionHandle(cookie.Value)); err == nil {
		t.Error("logged-out landing must delete the session row")
	}
}
