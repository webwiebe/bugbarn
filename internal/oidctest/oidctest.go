// Package oidctest provides an in-process fake OpenID Connect provider for
// tests: discovery + JWKS + a scriptable token endpoint, plus RS256-signed
// ID/logout token minting. It lets the OIDC client, session store, and HTTP
// middleware be exercised end-to-end (code exchange, refresh rotation,
// invalid_grant, IdP outages, back-channel logout) without a real iambarn.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// IdP is a fake OIDC provider backed by an httptest server.
type IdP struct {
	Key      *rsa.PrivateKey
	ClientID string

	mu           sync.Mutex
	server       *httptest.Server
	tokenHandler http.HandlerFunc
	revoked      []string
}

// New starts a fake IdP. Callers must Close it (t.Cleanup(idp.Close)).
// The default token endpoint answers 500 until SetTokenHandler is called.
func New(clientID string) (*IdP, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	p := &IdP{Key: key, ClientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.serveDiscovery)
	mux.HandleFunc("/jwks", p.serveJWKS)
	mux.HandleFunc("/oauth2/token", p.serveToken)
	mux.HandleFunc("/oauth2/revoke", p.serveRevoke)
	p.server = httptest.NewServer(mux)
	return p, nil
}

// Close shuts the httptest server down.
func (p *IdP) Close() { p.server.Close() }

// Issuer returns the fake issuer URL.
func (p *IdP) Issuer() string { return p.server.URL }

// SetTokenHandler scripts the token endpoint (code exchange and refresh
// grants both land here).
func (p *IdP) SetTokenHandler(h http.HandlerFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokenHandler = h
}

// Revoked returns the tokens posted to the revocation endpoint so far.
func (p *IdP) Revoked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.revoked...)
}

func (p *IdP) serveDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.server.URL,
		"authorization_endpoint":                p.server.URL + "/oauth2/authorize",
		"token_endpoint":                        p.server.URL + "/oauth2/token",
		"jwks_uri":                              p.server.URL + "/jwks",
		"end_session_endpoint":                  p.server.URL + "/oauth2/end-session",
		"revocation_endpoint":                   p.server.URL + "/oauth2/revoke",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *IdP) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := p.Key.Public().(*rsa.PublicKey)
	writeJSON(w, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": "test-key",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

func (p *IdP) serveToken(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	h := p.tokenHandler
	p.mu.Unlock()
	if h == nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)
		return
	}
	h(w, r)
}

func (p *IdP) serveRevoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p.mu.Lock()
	p.revoked = append(p.revoked, r.PostFormValue("token"))
	p.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// SignJWT signs an arbitrary claim set with the IdP key (RS256, kid test-key).
func (p *IdP) SignJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"test-key"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(fmt.Sprintf("oidctest: marshal claims: %v", err))
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.Key, crypto.SHA256, digest[:])
	if err != nil {
		panic(fmt.Sprintf("oidctest: sign: %v", err))
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// IDTokenClaims returns a baseline valid ID-token claim set that tests can
// tweak before SignJWT.
func (p *IdP) IDTokenClaims(sub, sid, nonce string, groups []string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":    p.server.URL,
		"aud":    p.ClientID,
		"sub":    sub,
		"sid":    sid,
		"nonce":  nonce,
		"groups": groups,
		"email":  sub + "@example.com",
		"iat":    now.Unix(),
		"exp":    now.Add(5 * time.Minute).Unix(),
	}
}

// LogoutToken mints a back-channel logout token; mutate (may be nil) can
// break individual claims to exercise rejection paths.
func (p *IdP) LogoutToken(sub, sid string, mutate func(map[string]any)) string {
	now := time.Now()
	claims := map[string]any{
		"iss": p.server.URL,
		"aud": p.ClientID,
		"iat": now.Unix(),
		"exp": now.Add(2 * time.Minute).Unix(),
		"jti": fmt.Sprintf("jti-%d", now.UnixNano()),
		"events": map[string]any{
			"http://schemas.openid.net/event/backchannel-logout": map[string]any{},
		},
	}
	if sub != "" {
		claims["sub"] = sub
	}
	if sid != "" {
		claims["sid"] = sid
	}
	if mutate != nil {
		mutate(claims)
	}
	return p.SignJWT(claims)
}

// WriteTokenResponse writes a standard token-endpoint success payload.
func WriteTokenResponse(w http.ResponseWriter, idToken, accessToken, refreshToken string, expiresIn int) {
	writeJSON(w, map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"id_token":      idToken,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
	})
}

// WriteTokenError writes a standard OAuth2 error payload (e.g. invalid_grant).
func WriteTokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
