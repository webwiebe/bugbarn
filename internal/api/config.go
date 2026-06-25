package api

import (
	"net/http"
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
			EndSessionURL:    s.oidc.LogoutURL(),
		}
		if issuer := s.oidc.Issuer(); issuer != "" {
			cfg.IAMBarn.ProfileURL = issuer + "/admin#profile"
		}
	}

	writeJSON(w, cfg)
}
