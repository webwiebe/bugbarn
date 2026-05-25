package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

// serveRuntimeConfig returns public (non-secret) configuration that the web
// frontend needs at startup. This endpoint requires no authentication so the
// frontend can fetch it before the user logs in.
//
// FunnelBarn analytics tracking is opt-in: the funnelbarn block is only
// enabled when BUGBARN_FUNNELBARN_ENDPOINT is set in the server's environment.
func (s *Server) serveRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	type funnelBarnConfig struct {
		Enabled  bool   `json:"enabled"`
		Endpoint string `json:"endpoint,omitempty"`
		APIKey   string `json:"apiKey,omitempty"`
	}

	type bugbarnSelfConfig struct {
		Enabled bool   `json:"enabled"`
		APIKey  string `json:"apiKey,omitempty"`
		Project string `json:"project,omitempty"`
	}

	type oidcConfigOut struct {
		Enabled          bool   `json:"enabled"`
		LoginURL         string `json:"loginURL,omitempty"`
		SwitchAccountURL string `json:"switchAccountURL,omitempty"`
		EndSessionURL    string `json:"endSessionURL,omitempty"`
	}

	type iambarnConfig struct {
		ProfileURL string `json:"profileURL,omitempty"`
	}

	type runtimeConfig struct {
		FunnelBarn funnelBarnConfig  `json:"funnelbarn"`
		BugBarn    bugbarnSelfConfig `json:"bugbarn"`
		OIDC       oidcConfigOut     `json:"oidc"`
		IAMBarn    iambarnConfig     `json:"iambarn"`
	}

	cfg := runtimeConfig{}
	if s.funnelBarnEndpoint != "" {
		cfg.FunnelBarn = funnelBarnConfig{
			Enabled:  true,
			Endpoint: s.funnelBarnEndpoint,
			APIKey:   s.funnelBarnAPIKey,
		}
	}
	if s.selfAPIKey != "" {
		cfg.BugBarn = bugbarnSelfConfig{
			Enabled: true,
			APIKey:  s.selfAPIKey,
			Project: s.selfProject,
		}
	}
	if s.oidc != nil {
		cfg.OIDC = oidcConfigOut{
			Enabled:          true,
			LoginURL:         "/api/v1/oidc/login",
			SwitchAccountURL: "/api/v1/oidc/login?prompt=login",
			EndSessionURL:    buildEndSessionURL(s.oidc.EndSessionURL(), s.oidc.Config()),
		}
		if issuer := strings.TrimRight(s.oidc.Config().Issuer, "/"); issuer != "" {
			cfg.IAMBarn.ProfileURL = issuer + "/admin#profile"
		}
	}

	writeJSON(w, cfg)
}

// buildEndSessionURL appends the OIDC client_id and a post_logout_redirect_uri
// pointing at this barn's origin to the issuer's end-session endpoint.
// iambarn (and the OIDC spec) require the URI to be allowlisted on the
// identified client; without client_id the IdP falls through to a default
// redirect (in iambarn's case, back to its own root), stranding the user on
// the IdP page instead of returning them here.
func buildEndSessionURL(raw string, cfg auth.OIDCConfig) string {
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
