package oidctest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newIdP(t *testing.T) *IdP {
	t.Helper()
	idp, err := New("client-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(idp.Close)
	return idp
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestJWKSPublishesTheSigningKeyAndVerifiesSignedTokens(t *testing.T) {
	idp := newIdP(t)

	jwks := getJSON(t, idp.Issuer()+"/jwks")
	keys := jwks["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("jwks has %d keys, want 1", len(keys))
	}
	k := keys[0].(map[string]any)
	if k["kid"] != "test-key" || k["alg"] != "RS256" {
		t.Fatalf("jwk = %v", k)
	}
	n, err := base64.RawURLEncoding.DecodeString(k["n"].(string))
	if err != nil {
		t.Fatal(err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k["e"].(string))
	if err != nil {
		t.Fatal(err)
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}

	tok := idp.SignJWT(map[string]any{"sub": "alice"})
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("published key does not verify the signature: %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "alice" {
		t.Fatalf("claims = %v", claims)
	}
}

func TestSetTokenHandlerScriptsTheTokenEndpoint(t *testing.T) {
	idp := newIdP(t)
	idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
		WriteTokenResponse(w, "id-1", "access-1", "refresh-1", 300)
	})

	res, err := http.Post(idp.Issuer()+"/oauth2/token", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["id_token"] != "id-1" || body["access_token"] != "access-1" ||
		body["refresh_token"] != "refresh-1" || body["token_type"] != "Bearer" ||
		body["expires_in"] != float64(300) {
		t.Fatalf("token response = %v", body)
	}

	idp.SetTokenHandler(func(w http.ResponseWriter, _ *http.Request) {
		WriteTokenError(w, http.StatusBadRequest, "invalid_grant")
	})
	res2, err := http.Post(idp.Issuer()+"/oauth2/token", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res2.StatusCode)
	}
	var errBody map[string]string
	if err := json.NewDecoder(res2.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if errBody["error"] != "invalid_grant" {
		t.Fatalf("error body = %v", errBody)
	}
}

func TestLogoutTokenOmitsEmptySubjectAndAppliesMutations(t *testing.T) {
	idp := newIdP(t)

	decode := func(tok string) map[string]any {
		payload, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
		if err != nil {
			t.Fatal(err)
		}
		var c map[string]any
		if err := json.Unmarshal(payload, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	c := decode(idp.LogoutToken("", "", func(c map[string]any) { c["aud"] = "other" }))
	if _, ok := c["sub"]; ok {
		t.Error("empty sub must be omitted")
	}
	if _, ok := c["sid"]; ok {
		t.Error("empty sid must be omitted")
	}
	if c["aud"] != "other" {
		t.Errorf("mutation not applied: aud = %v", c["aud"])
	}
	if c["iss"] != idp.Issuer() {
		t.Errorf("iss = %v", c["iss"])
	}
}

func TestSignJWTPanicsOnUnmarshalableClaims(t *testing.T) {
	idp := newIdP(t)
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for claims that cannot be marshaled")
		}
	}()
	idp.SignJWT(map[string]any{"bad": make(chan int)})
}

func TestWriteTokenErrorSetsJSONContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteTokenError(rec, http.StatusUnauthorized, "invalid_client")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
}
