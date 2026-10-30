package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
)

// oidcCallbackWith drives one login whose ID token is the baseline claim set
// after mutate.
func oidcCallbackWith(t *testing.T, server *Server, idp *oidctest.IdP, mutate func(map[string]any)) *httptest.ResponseRecorder {
	t.Helper()
	authorize, cookies := startOIDCLogin(t, server, "")
	nonce := cookieValue(cookies, oidcNonceCookie)
	claims := idp.IDTokenClaims("sub-1", "sid-1", nonce, nil)
	mutate(claims)
	idToken := idp.SignJWT(claims)
	idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
		oidctest.WriteTokenResponse(w, idToken, "at-1", "rt-1", 900)
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/oidc/callback?code=code-1&state="+url.QueryEscape(authorize.Query().Get("state")), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	server.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("callback = %d body=%q", rr.Code, rr.Body.String())
	}
	return rr
}

func TestOIDCCallbackAdmitsAdminsOfThisOrganization(t *testing.T) {
	for _, role := range []string{"owner", "organization_admin", "operator"} {
		t.Run(role, func(t *testing.T) {
			server, idp, _ := newOIDCServer(t)
			rr := oidcCallbackWith(t, server, idp, func(c map[string]any) { c["roles"] = []string{role} })
			session := cookieValue(rr.Result().Cookies(), "bugbarn_session")
			if session == "" {
				t.Fatalf("expected a session, redirected to %q", rr.Header().Get("Location"))
			}
			if me := getMe(server, &http.Cookie{Name: "bugbarn_session", Value: session}); me.Code != http.StatusOK {
				t.Fatalf("me = %d", me.Code)
			}
		})
	}
}

func TestOIDCCallbackRefusesWithoutASession(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{"owner of another organization", func(c map[string]any) { c["organization_id"] = "org-other" }, "wrong_organization"},
		{"operator of another organization", func(c map[string]any) {
			c["organization_id"] = "org-other"
			c["roles"] = []string{"operator"}
		}, "wrong_organization"},
		{"owner with no organization claim", func(c map[string]any) { delete(c, "organization_id") }, "wrong_organization"},
		{"project group in this organization", func(c map[string]any) {
			c["roles"] = []string{"member"}
			c["groups"] = []string{"bugbarn-users"}
		}, "access_denied"},
		{"no role in this organization", func(c map[string]any) { delete(c, "roles") }, "access_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, idp, _ := newOIDCServer(t)
			rr := oidcCallbackWith(t, server, idp, tc.mutate)
			assertOIDCRefused(t, rr, tc.code)
		})
	}
}

func TestOIDCCallbackRefusesAnOwnerWhenNoOrganizationIsConfigured(t *testing.T) {
	server, idp, _ := newOIDCServer(t)
	server.SetOIDCClient(auth.NewOIDCClient(auth.OIDCConfig{
		Issuer:       idp.Issuer(),
		ClientID:     "bugbarn-test",
		ClientSecret: "sek",
		RedirectURL:  "http://bugbarn.example.com/api/v1/oidc/callback",
	}))
	rr := oidcCallbackWith(t, server, idp, func(map[string]any) {})
	assertOIDCRefused(t, rr, "access_denied")
}

func assertOIDCRefused(t *testing.T, rr *httptest.ResponseRecorder, code string) {
	t.Helper()
	if session := cookieValue(rr.Result().Cookies(), "bugbarn_session"); session != "" {
		t.Error("a refused sign-in must not create a session")
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loc.Query().Get("oidc_error"); got != code {
		t.Errorf("oidc_error = %q, want %q (redirect %q)", got, code, loc)
	}
}
