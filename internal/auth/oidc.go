// Package auth contains the OIDC login adapter used when BUGBARN_OIDC_* env
// vars are set. Local single-user login (BUGBARN_ADMIN_PASSWORD / _BCRYPT)
// remains the zero-config default — OIDC is an additional, opt-in flow.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	oidcv3 "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig is the static configuration for the OIDC login flow.
type OIDCConfig struct {
	Issuer        string // iambarn issuer URL, e.g. https://iam.wiebe.xyz
	ClientID      string
	ClientSecret  string
	RedirectURL   string // e.g. https://bugbarn.wiebe.xyz/api/v1/oidc/callback
	RequiredGroup string // group slug that grants access; bypass roles always win
}

// Enabled reports whether all four required fields are present.
func (c OIDCConfig) Enabled() bool {
	return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// OIDCClient lazily discovers the issuer's OpenID configuration and exposes
// the few primitives the HTTP layer needs: an authorize URL, a code-exchange,
// and an ID-token verification that also enforces the access policy.
type OIDCClient struct {
	cfg     OIDCConfig
	timeout time.Duration

	mu       sync.Mutex
	provider *oidcv3.Provider
	verifier *oidcv3.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDCClient returns a client. Discovery is deferred to the first call so
// that an unreachable issuer at startup does not crash the process.
func NewOIDCClient(cfg OIDCConfig) *OIDCClient {
	if cfg.RequiredGroup == "" {
		cfg.RequiredGroup = "bugbarn-users"
	}
	return &OIDCClient{cfg: cfg, timeout: 10 * time.Second}
}

// Config returns the static configuration. Used to expose non-secret bits
// (login URL) via runtime-config.
func (c *OIDCClient) Config() OIDCConfig { return c.cfg }

// Issuer returns the configured issuer URL with any trailing slash trimmed.
func (c *OIDCClient) Issuer() string { return strings.TrimRight(c.cfg.Issuer, "/") }

// LogoutURL returns the issuer's RP-initiated logout endpoint with this client's
// client_id and a post-logout redirect back to this barn's origin appended, or
// the bare end-session URL when those parameters can't be derived. Callers get
// exactly the URL to use without reaching into the client's configuration.
func (c *OIDCClient) LogoutURL() string {
	return buildEndSessionURL(c.EndSessionURL(), c.cfg)
}

// buildEndSessionURL appends the OIDC client_id and a post_logout_redirect_uri
// pointing at this barn's origin to the issuer's end-session endpoint.
// iambarn (and the OIDC spec) require the URI to be allowlisted on the
// identified client; without client_id the IdP falls through to a default
// redirect (in iambarn's case, back to its own root), stranding the user on
// the IdP page instead of returning them here.
func buildEndSessionURL(raw string, cfg OIDCConfig) string {
	if raw == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return raw
	}
	u, err := url.Parse(cfg.RedirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	postLogout := u.Scheme + "://" + u.Host + "/"
	q := url.Values{}
	q.Set("client_id", cfg.ClientID)
	q.Set("post_logout_redirect_uri", postLogout)
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + q.Encode()
}

// AuthorizeURL builds the URL the browser should be redirected to. The caller
// is responsible for storing state + nonce in short-lived cookies and matching
// them on callback. If prompt is non-empty (e.g. "login", "select_account"),
// it is forwarded to the IdP so the user is re-prompted instead of silently
// reusing an existing IdP session.
func (c *OIDCClient) AuthorizeURL(state, nonce, prompt string) (string, error) {
	if err := c.ensureReady(context.Background()); err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oidcv3.Nonce(nonce)}
	if prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	return c.oauth.AuthCodeURL(state, opts...), nil
}

// EndSessionURL returns the issuer's RP-initiated logout endpoint, or "" if
// the issuer's discovery document doesn't advertise one.
func (c *OIDCClient) EndSessionURL() string {
	if err := c.ensureReady(context.Background()); err != nil {
		return ""
	}
	var meta struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := c.provider.Claims(&meta); err != nil {
		return ""
	}
	return meta.EndSessionEndpoint
}

// Exchange swaps an authorization code for tokens and verifies the ID token's
// signature + audience + nonce. Returns the parsed ID-token claims on success.
func (c *OIDCClient) Exchange(ctx context.Context, code, nonce string) (Claims, error) {
	if err := c.ensureReady(ctx); err != nil {
		return Claims{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return Claims{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Claims{}, errors.New("oidc: token response missing id_token")
	}
	idToken, err := c.verifier.Verify(ctx, rawID)
	if err != nil {
		return Claims{}, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return Claims{}, errors.New("oidc: nonce mismatch")
	}
	var claims Claims
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, fmt.Errorf("oidc: decode claims: %w", err)
	}
	return claims, nil
}

// Allowed returns true if the claims grant access to this barn.
// Owner/organization_admin/operator roles bypass the group check.
func (c *OIDCClient) Allowed(claims Claims) bool {
	for _, role := range claims.Roles {
		switch role {
		case "owner", "organization_admin", "operator":
			return true
		}
	}
	for _, g := range claims.Groups {
		if g == c.cfg.RequiredGroup {
			return true
		}
	}
	return false
}

// ensureReady performs the one-time discovery + provider wiring. Safe for
// concurrent callers.
func (c *OIDCClient) ensureReady(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	prov, err := oidcv3.NewProvider(ctx, strings.TrimRight(c.cfg.Issuer, "/"))
	if err != nil {
		return fmt.Errorf("oidc: discover issuer %q: %w", c.cfg.Issuer, err)
	}
	c.provider = prov
	c.verifier = prov.Verifier(&oidcv3.Config{ClientID: c.cfg.ClientID})
	c.oauth = &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  c.cfg.RedirectURL,
		Scopes:       []string{oidcv3.ScopeOpenID, "profile", "email"},
	}
	return nil
}

// Claims is the subset of ID-token claims this barn cares about.
type Claims struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	Roles             []string `json:"roles"`
}

// PreferredName returns the best human-readable identifier from the claims.
func (c Claims) PreferredName() string {
	for _, v := range []string{c.PreferredUsername, c.Email, c.Name, c.Subject} {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
