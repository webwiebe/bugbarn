// Package auth contains the OIDC login adapter used when BUGBARN_OIDC_* env
// vars are set. Local single-user login (BUGBARN_ADMIN_PASSWORD / _BCRYPT)
// remains the zero-config default — OIDC is an additional, opt-in flow.
package auth

import (
	"context"
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
	// Optional endpoints from discovery, with conventional iambarn fallbacks
	// when the discovery document omits them.
	revocationEndpoint string
	endSessionEndpoint string
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

// LogoutURLWithIDTokenHint builds the RP-initiated logout URL for a specific
// session: id_token_hint identifies the IdP session to end without any
// interactive confirmation, client_id + post_logout_redirect_uri bring the
// browser back to this barn's /api/v1/oidc/logged-out landing (which clears
// the local cookies). Returns "" when the issuer can't be reached.
func (c *OIDCClient) LogoutURLWithIDTokenHint(idTokenHint string) string {
	endSession := c.EndSessionURL()
	if endSession == "" {
		return ""
	}
	q := url.Values{}
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	q.Set("client_id", c.cfg.ClientID)
	if postLogout := c.PostLogoutRedirectURI(); postLogout != "" {
		q.Set("post_logout_redirect_uri", postLogout)
	}
	sep := "?"
	if strings.Contains(endSession, "?") {
		sep = "&"
	}
	return endSession + sep + q.Encode()
}

// PostLogoutRedirectURI returns the absolute URL the IdP should send the browser
// back to after an RP-initiated logout. It points at this barn's own
// /api/v1/oidc/logged-out endpoint (derived from the configured redirect URL's
// origin), which clears the local session before returning the user to the app.
// Returns "" when the redirect URL can't be parsed. This URI must be registered
// on the OIDC client's post-logout allowlist. Used both by the hosted IAMBarn
// widgets (via runtime-config) and by buildEndSessionURL.
func (c *OIDCClient) PostLogoutRedirectURI() string {
	origin := originOf(c.cfg.RedirectURL)
	if origin == "" {
		return ""
	}
	return origin + "/api/v1/oidc/logged-out"
}

// originOf returns scheme://host for a URL, or "" if it can't be parsed.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
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
	origin := originOf(cfg.RedirectURL)
	if origin == "" {
		return raw
	}
	postLogout := origin + "/"
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
// is responsible for storing state + nonce (and the PKCE verifier) in
// short-lived cookies and matching them on callback. If prompt is non-empty
// (e.g. "login", "select_account"), it is forwarded to the IdP so the user is
// re-prompted instead of silently reusing an existing IdP session. verifier is
// the PKCE code_verifier from oauth2.GenerateVerifier; its S256 challenge is
// sent with the authorization request even though BugBarn is a confidential
// client (PKCE-everywhere hardens against code injection at zero cost).
func (c *OIDCClient) AuthorizeURL(state, nonce, prompt, verifier string) (string, error) {
	if err := c.ensureReady(context.Background()); err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oidcv3.Nonce(nonce)}
	if prompt != "" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", prompt))
	}
	if verifier != "" {
		opts = append(opts, oauth2.S256ChallengeOption(verifier))
	}
	return c.oauth.AuthCodeURL(state, opts...), nil
}

// EndSessionURL returns the issuer's RP-initiated logout endpoint, or "" if
// the issuer can't be reached for discovery.
func (c *OIDCClient) EndSessionURL() string {
	if err := c.ensureReady(context.Background()); err != nil {
		return ""
	}
	return c.endSessionEndpoint
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
	// Revocation + end-session endpoints are optional discovery fields; fall
	// back to iambarn's conventional paths when the document omits them.
	issuer := strings.TrimRight(c.cfg.Issuer, "/")
	var extra struct {
		RevocationEndpoint string `json:"revocation_endpoint"`
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = prov.Claims(&extra)
	c.revocationEndpoint = extra.RevocationEndpoint
	if c.revocationEndpoint == "" {
		c.revocationEndpoint = issuer + "/oauth2/revoke"
	}
	c.endSessionEndpoint = extra.EndSessionEndpoint
	if c.endSessionEndpoint == "" {
		c.endSessionEndpoint = issuer + "/oauth2/end-session"
	}
	c.verifier = prov.Verifier(&oidcv3.Config{ClientID: c.cfg.ClientID})
	c.oauth = &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  c.cfg.RedirectURL,
		// offline_access asks iambarn for a refresh_token alongside the
		// short-lived access_token so the session can be renewed silently.
		// It must be requested explicitly — iambarn only grants what is both
		// allowed on the client record AND present in the request.
		Scopes: []string{oidcv3.ScopeOpenID, "profile", "email", "offline_access"},
	}
	return nil
}

// Claims is the subset of ID-token claims this barn cares about.
type Claims struct {
	Subject           string   `json:"sub"`
	SessionID         string   `json:"sid"` // IdP session id; keys back-channel logout
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
