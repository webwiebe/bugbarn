package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
)

// callbackRequest builds a callback request that carries the flow cookies from
// startOIDCLogin, optionally dropping one of them.
func callbackRequest(path string, cookies []*http.Cookie, drop string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		if c.Name != drop {
			req.AddCookie(c)
		}
	}
	return req
}

func TestOIDCCallbackErrorRedirects(t *testing.T) {
	server, _, _ := newOIDCServer(t)

	cases := []struct {
		idpError string
		wantPath string
		wantKey  string
		wantVal  string
	}{
		{"login_required", "/api/v1/oidc/login", "", ""},
		{"interaction_required", "/api/v1/oidc/login", "", ""},
		{"access_denied", "/app/", "oidc_error", "access_denied"},
		{"server_error", "/app/", "oidc_error", "server_error"},
	}
	for _, tc := range cases {
		t.Run(tc.idpError, func(t *testing.T) {
			authorize, cookies := startOIDCLogin(t, server, "")
			state := authorize.Query().Get("state")
			rr := httptest.NewRecorder()
			server.ServeHTTP(rr, callbackRequest(
				"/api/v1/oidc/callback?error="+tc.idpError+"&state="+url.QueryEscape(state), cookies, ""))
			if rr.Code != http.StatusFound {
				t.Fatalf("callback = %d, want 302", rr.Code)
			}
			loc, err := url.Parse(rr.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if loc.Path != tc.wantPath {
				t.Errorf("redirect path = %q, want %q", loc.Path, tc.wantPath)
			}
			if tc.wantKey != "" && loc.Query().Get(tc.wantKey) != tc.wantVal {
				t.Errorf("redirect query %s = %q, want %q", tc.wantKey, loc.Query().Get(tc.wantKey), tc.wantVal)
			}
			if cookieValue(rr.Result().Cookies(), oidcStateCookie) != "" {
				t.Error("state cookie must be cleared after an error redirect")
			}
		})
	}
}

func TestOIDCCallbackSilentReauthKeepsReturnTo(t *testing.T) {
	server, _, _ := newOIDCServer(t)
	authorize, cookies := startOIDCLogin(t, server, "?return_to=%2Fapp%2F%23%2Faccount")
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, callbackRequest(
		"/api/v1/oidc/callback?error=login_required&state="+url.QueryEscape(authorize.Query().Get("state")), cookies, ""))
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("return_to") != "/app/#/account" {
		t.Errorf("return_to = %q, want the original destination", loc.Query().Get("return_to"))
	}
}

func TestOIDCCallbackRejectsIncompleteRequests(t *testing.T) {
	server, _, _ := newOIDCServer(t)

	t.Run("missing nonce cookie", func(t *testing.T) {
		authorize, cookies := startOIDCLogin(t, server, "")
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, callbackRequest(
			"/api/v1/oidc/callback?code=c&state="+url.QueryEscape(authorize.Query().Get("state")), cookies, oidcNonceCookie))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "nonce") {
			t.Errorf("= %d %q, want 400 naming the nonce", rr.Code, rr.Body.String())
		}
	})
	t.Run("missing code", func(t *testing.T) {
		authorize, cookies := startOIDCLogin(t, server, "")
		rr := httptest.NewRecorder()
		server.ServeHTTP(rr, callbackRequest(
			"/api/v1/oidc/callback?state="+url.QueryEscape(authorize.Query().Get("state")), cookies, ""))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "code") {
			t.Errorf("= %d %q, want 400 naming the code", rr.Code, rr.Body.String())
		}
	})
	t.Run("not configured", func(t *testing.T) {
		bare := NewServer(nil, mustOpenStore(t), nil)
		rr := httptest.NewRecorder()
		bare.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/oidc/callback?code=c", nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("= %d, want 404", rr.Code)
		}
	})
}

func TestOIDCCallbackExchangeFailureIs401(t *testing.T) {
	server, idp, _ := newOIDCServer(t)
	authorize, cookies := startOIDCLogin(t, server, "")
	idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
		oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_grant")
	})
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, callbackRequest(
		"/api/v1/oidc/callback?code=bad&state="+url.QueryEscape(authorize.Query().Get("state")), cookies, ""))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("exchange failure = %d, want 401", rr.Code)
	}
	if cookieValue(rr.Result().Cookies(), "bugbarn_session") != "" {
		t.Error("a failed exchange must not set a session cookie")
	}
}

func TestOIDCCallbackDeniedByGroupRedirectsWithIdentity(t *testing.T) {
	server, idp, _ := newOIDCServer(t)
	authorize, cookies := startOIDCLogin(t, server, "")
	nonce := cookieValue(cookies, oidcNonceCookie)
	idToken := idp.SignJWT(idp.IDTokenClaims("sub-x", "sid-x", nonce, []string{"someone-else"}))
	idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
		oidctest.WriteTokenResponse(w, idToken, "at", "rt", 900)
	})
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, callbackRequest(
		"/api/v1/oidc/callback?code=c&state="+url.QueryEscape(authorize.Query().Get("state")), cookies, ""))
	if rr.Code != http.StatusFound {
		t.Fatalf("denied login = %d, want 302", rr.Code)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Path != "/app/" || loc.Query().Get("oidc_error") != "access_denied" {
		t.Errorf("redirect = %q, want the app with oidc_error=access_denied", loc.String())
	}
	if cookieValue(rr.Result().Cookies(), "bugbarn_session") != "" {
		t.Error("a denied login must not set a session cookie")
	}
}
