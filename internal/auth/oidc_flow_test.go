package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/oidctest"
)

const testClientID = "bugbarn-test"

func newClient(t *testing.T) (*oidctest.IdP, *auth.OIDCClient) {
	t.Helper()
	idp, err := oidctest.New(testClientID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	client := auth.NewOIDCClient(auth.OIDCConfig{
		Issuer:       idp.Issuer(),
		ClientID:     testClientID,
		ClientSecret: "sek",
		RedirectURL:  "https://bugbarn.example.com/api/v1/oidc/callback",
	})
	return idp, client
}

func TestAuthorizeURLIncludesPKCEAndOfflineAccess(t *testing.T) {
	_, client := newClient(t)
	raw, err := client.AuthorizeURL("state-1", "nonce-1", "login", "verifier-verifier-verifier-verifier-1234567")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("missing PKCE challenge: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "offline_access") {
		t.Errorf("scope %q missing offline_access", q.Get("scope"))
	}
	if q.Get("prompt") != "login" || q.Get("nonce") != "nonce-1" || q.Get("state") != "state-1" {
		t.Errorf("unexpected params: %v", q)
	}
}

func TestExchangeFull(t *testing.T) {
	idp, client := newClient(t)
	idToken := idp.SignJWT(idp.IDTokenClaims("sub-1", "sid-1", "nonce-1", []string{"bugbarn-users"}))
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("grant_type") != "authorization_code" || r.PostFormValue("code") != "code-1" {
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if r.PostFormValue("code_verifier") == "" {
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		oidctest.WriteTokenResponse(w, idToken, "at-1", "rt-1", 900)
	})

	res, err := client.ExchangeFull(context.Background(), "code-1", "nonce-1", "verifier-verifier-verifier-verifier-1234567")
	if err != nil {
		t.Fatal(err)
	}
	if res.Claims.Subject != "sub-1" || res.Claims.SessionID != "sid-1" {
		t.Errorf("claims = %+v", res.Claims)
	}
	if res.AccessToken != "at-1" || res.RefreshToken != "rt-1" || res.IDToken != idToken {
		t.Errorf("tokens = %+v", res)
	}
	if time.Until(res.ExpiresAt) < 10*time.Minute {
		t.Errorf("expiry = %v", res.ExpiresAt)
	}

	// A tampered nonce must be rejected even with a valid signature.
	if _, err := client.ExchangeFull(context.Background(), "code-1", "other-nonce", "verifier-verifier-verifier-verifier-1234567"); err == nil {
		t.Error("expected nonce mismatch to fail")
	}
}

func TestRefreshRotationAndInvalidGrant(t *testing.T) {
	idp, client := newClient(t)
	freshID := idp.SignJWT(idp.IDTokenClaims("sub-1", "sid-1", "", []string{"new-group"}))
	idp.SetTokenHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.PostFormValue("refresh_token") {
		case "rt-old":
			oidctest.WriteTokenResponse(w, freshID, "at-2", "rt-new", 900)
		case "rt-dead":
			oidctest.WriteTokenError(w, http.StatusBadRequest, "invalid_grant")
		default:
			oidctest.WriteTokenError(w, http.StatusInternalServerError, "server_error")
		}
	})

	refreshed, err := client.Refresh(context.Background(), "rt-old")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "at-2" || refreshed.RefreshToken != "rt-new" {
		t.Errorf("rotation result = %+v", refreshed)
	}
	if refreshed.Claims == nil || len(refreshed.Claims.Groups) != 1 || refreshed.Claims.Groups[0] != "new-group" {
		t.Errorf("expected re-snapshotted claims, got %+v", refreshed.Claims)
	}

	if _, err := client.Refresh(context.Background(), "rt-dead"); !errors.Is(err, auth.ErrRefreshInvalid) {
		t.Errorf("invalid_grant: got %v, want ErrRefreshInvalid", err)
	}
	if _, err := client.Refresh(context.Background(), "rt-outage"); err == nil || errors.Is(err, auth.ErrRefreshInvalid) {
		t.Errorf("5xx should be a transient error, got %v", err)
	}
}

func TestRevokeRefreshToken(t *testing.T) {
	idp, client := newClient(t)
	if err := client.RevokeRefreshToken(context.Background(), "rt-gone"); err != nil {
		t.Fatal(err)
	}
	if got := idp.Revoked(); len(got) != 1 || got[0] != "rt-gone" {
		t.Errorf("Revoked() = %v", got)
	}
	// Empty token is a no-op, not an error.
	if err := client.RevokeRefreshToken(context.Background(), ""); err != nil {
		t.Errorf("empty revoke: %v", err)
	}
}

func TestVerifyLogoutToken(t *testing.T) {
	idp, client := newClient(t)
	ctx := context.Background()

	claims, err := client.VerifyLogoutToken(ctx, idp.LogoutToken("sub-1", "sid-1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "sub-1" || claims.SessionID != "sid-1" {
		t.Errorf("claims = %+v", claims)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"nonce present", func(c map[string]any) { c["nonce"] = "n" }},
		{"missing events", func(c map[string]any) { delete(c, "events") }},
		{"stale iat", func(c map[string]any) { c["iat"] = time.Now().Add(-10 * time.Minute).Unix() }},
		{"neither sub nor sid", func(c map[string]any) { delete(c, "sub"); delete(c, "sid") }},
		{"wrong audience", func(c map[string]any) { c["aud"] = "someone-else" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.VerifyLogoutToken(ctx, idp.LogoutToken("sub-1", "sid-1", tc.mutate)); err == nil {
				t.Error("expected rejection")
			}
		})
	}
}

func TestLogoutURLWithIDTokenHint(t *testing.T) {
	_, client := newClient(t)
	raw := client.LogoutURLWithIDTokenHint("idtok-1")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("id_token_hint") != "idtok-1" || q.Get("client_id") != testClientID {
		t.Errorf("logout url params: %v", q)
	}
	if q.Get("post_logout_redirect_uri") != "https://bugbarn.example.com/api/v1/oidc/logged-out" {
		t.Errorf("post_logout_redirect_uri = %q", q.Get("post_logout_redirect_uri"))
	}
	if !strings.HasSuffix(u.Path, "/oauth2/end-session") {
		t.Errorf("path = %q", u.Path)
	}
}
