package api

import "net/http"

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

	type runtimeConfig struct {
		FunnelBarn funnelBarnConfig `json:"funnelbarn"`
	}

	cfg := runtimeConfig{}
	if s.funnelBarnEndpoint != "" {
		cfg.FunnelBarn = funnelBarnConfig{
			Enabled:  true,
			Endpoint: s.funnelBarnEndpoint,
			APIKey:   s.funnelBarnAPIKey,
		}
	}

	writeJSON(w, cfg)
}
