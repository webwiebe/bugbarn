package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

// TestRuntimeConfigIAMBarn verifies that the runtime-config endpoint exposes the
// non-secret values the hosted IAMBarn widgets need (serverURL, clientID,
// postLogoutRedirectURI) when OIDC is configured, and omits the block otherwise.
func TestRuntimeConfigIAMBarn(t *testing.T) {
	t.Parallel()

	store := mustOpenStore(t)
	defer store.Close()

	// Fake IAMBarn issuer advertising an end_session_endpoint so LogoutURL()
	// resolves without reaching the network.
	var issuerURL string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"issuer": %q,
				"authorization_endpoint": %q,
				"token_endpoint": %q,
				"jwks_uri": %q,
				"end_session_endpoint": %q
			}`, issuerURL, issuerURL+"/authorize", issuerURL+"/token", issuerURL+"/jwks", issuerURL+"/oauth2/end-session")
			return
		}
		http.NotFound(w, r)
	}))
	defer issuer.Close()
	issuerURL = issuer.URL

	type iambarnBlock struct {
		ServerURL             string `json:"serverURL"`
		ClientID              string `json:"clientID"`
		PostLogoutRedirectURI string `json:"postLogoutRedirectURI"`
	}
	type oidcBlock struct {
		Enabled       bool   `json:"enabled"`
		EndSessionURL string `json:"endSessionURL"`
	}
	type runtimeConfig struct {
		OIDC    oidcBlock    `json:"oidc"`
		IAMBarn iambarnBlock `json:"iambarn"`
	}

	get := func(t *testing.T, server *Server) runtimeConfig {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-config", nil)
		server.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("runtime-config status = %d, want 200", rr.Code)
		}
		var cfg runtimeConfig
		if err := json.Unmarshal(rr.Body.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	t.Run("oidc configured", func(t *testing.T) {
		server := NewServer(nil, store, nil)
		server.SetOIDCClient(auth.NewOIDCClient(auth.OIDCConfig{
			Issuer:       issuerURL,
			ClientID:     "bugbarn-web",
			ClientSecret: "sek",
			RedirectURL:  "https://bugbarn.example.com/api/v1/oidc/callback",
		}))

		cfg := get(t, server)

		if !cfg.OIDC.Enabled {
			t.Errorf("oidc.enabled = false, want true")
		}
		if cfg.IAMBarn.ServerURL != issuerURL {
			t.Errorf("iambarn.serverURL = %q, want %q", cfg.IAMBarn.ServerURL, issuerURL)
		}
		if cfg.IAMBarn.ClientID != "bugbarn-web" {
			t.Errorf("iambarn.clientID = %q, want %q", cfg.IAMBarn.ClientID, "bugbarn-web")
		}
		want := "https://bugbarn.example.com/api/v1/oidc/logged-out"
		if cfg.IAMBarn.PostLogoutRedirectURI != want {
			t.Errorf("iambarn.postLogoutRedirectURI = %q, want %q", cfg.IAMBarn.PostLogoutRedirectURI, want)
		}
	})

	t.Run("oidc absent", func(t *testing.T) {
		server := NewServer(nil, store, nil)
		cfg := get(t, server)
		if cfg.OIDC.Enabled {
			t.Errorf("oidc.enabled = true, want false")
		}
		if cfg.IAMBarn != (iambarnBlock{}) {
			t.Errorf("iambarn block = %+v, want empty", cfg.IAMBarn)
		}
	})
}
