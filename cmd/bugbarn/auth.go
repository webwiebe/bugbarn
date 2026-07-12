package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func buildOIDCClient(cfg config.Config, logger *slog.Logger) *auth.OIDCClient {
	oc := auth.OIDCConfig{
		Issuer:        cfg.OIDCIssuer,
		ClientID:      cfg.OIDCClientID,
		ClientSecret:  cfg.OIDCClientSecret,
		RedirectURL:   cfg.OIDCRedirectURL,
		RequiredGroup: cfg.OIDCRequiredGroup,
	}
	if !oc.Enabled() {
		return nil
	}
	logger.Info("oidc: enabled", "issuer", oc.Issuer, "client_id", oc.ClientID, "required_group", oc.RequiredGroup)
	return auth.NewOIDCClient(oc)
}

// ensureProductionAPIKeyAuth refuses to boot a production instance whose API
// authorizer ended up with no mechanism at all — auth.Authorizer fails OPEN
// per request when disabled (ValidWithProject returns success), so the guard
// belongs at startup where a misconfiguration is loud instead of silently
// exposing every keyed endpoint.
func ensureProductionAPIKeyAuth(cfg config.Config, authorizer *auth.Authorizer) error {
	if !cfg.IsProduction() || authorizer.Enabled() {
		return nil
	}
	return errors.New("refusing to start: BUGBARN_ENVIRONMENT=production requires an API-key mechanism " +
		"(set BUGBARN_API_KEY or BUGBARN_API_KEY_SHA256, or wire the database key store)")
}

func newAPIAuthorizer(cfg config.Config, store *storage.Store) (*auth.Authorizer, error) {
	var base *auth.Authorizer
	var err error
	if cfg.APIKeySHA256 != "" {
		base, err = auth.NewHashed(cfg.APIKeySHA256)
		if err != nil {
			return nil, err
		}
	} else {
		base = auth.New(cfg.APIKey)
	}
	base = base.WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	if cfg.SessionSecret != "" {
		base = base.WithSetupKeyVerifier(newSetupKeyVerifier(cfg.SessionSecret))
	}
	return base, nil
}

// newSetupKeyVerifier returns a verifier that authorizes a setup key purely by
// its HMAC — it performs no database writes, so it is safe on the read-only
// reader pods that serve public ingest (CQRS split). The project is created
// (pending, awaiting approval) lazily on the writer when the forwarded event is
// consumed; see ingestproc.Processor.EnsureProjectForIngest. ProjectID is 0
// because the reader resolves projects by slug downstream, not at auth time.
func newSetupKeyVerifier(secret string) auth.SetupKeyVerifier {
	return func(_ context.Context, rawKey, projectSlug string) (int64, bool) {
		expected := setupKey(secret, projectSlug)
		if expected == "" || subtle.ConstantTimeCompare([]byte(rawKey), []byte(expected)) != 1 {
			return 0, false
		}
		return 0, true
	}
}

func setupKey(secret, slug string) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("setup:" + slug))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

// runWorkerOnce replays queued records into the persistent store for local maintenance.
