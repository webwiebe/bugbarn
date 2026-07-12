package oidctest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFakeIdPEndpoints(t *testing.T) {
	idp, err := New("client-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)

	res, err := http.Get(idp.Issuer() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var disc map[string]any
	if err := json.NewDecoder(res.Body).Decode(&disc); err != nil {
		t.Fatal(err)
	}
	if disc["issuer"] != idp.Issuer() {
		t.Errorf("issuer = %v", disc["issuer"])
	}
	if disc["revocation_endpoint"] != idp.Issuer()+"/oauth2/revoke" {
		t.Errorf("revocation_endpoint = %v", disc["revocation_endpoint"])
	}

	// Token endpoint defaults to 500 until scripted.
	tokRes, err := http.PostForm(idp.Issuer()+"/oauth2/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, tokRes.Body)
	tokRes.Body.Close()
	if tokRes.StatusCode != http.StatusInternalServerError {
		t.Errorf("unscripted token endpoint = %d", tokRes.StatusCode)
	}

	// Revocations are recorded.
	revRes, err := http.PostForm(idp.Issuer()+"/oauth2/revoke", map[string][]string{"token": {"rt-1"}})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, revRes.Body)
	revRes.Body.Close()
	if got := idp.Revoked(); len(got) != 1 || got[0] != "rt-1" {
		t.Errorf("Revoked() = %v", got)
	}

	// Minted tokens are three-part JWTs with the expected claims baseline.
	tok := idp.LogoutToken("sub-1", "sid-1", nil)
	if strings.Count(tok, ".") != 2 {
		t.Errorf("logout token is not a JWT: %q", tok)
	}
	claims := idp.IDTokenClaims("sub-1", "sid-1", "nonce-1", []string{"g"})
	if claims["aud"] != "client-1" || claims["sid"] != "sid-1" {
		t.Errorf("IDTokenClaims baseline = %v", claims)
	}
}
