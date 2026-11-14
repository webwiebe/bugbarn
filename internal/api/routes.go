package api

import (
	"net/http"
	"strings"
)

// Routing lives in the routes*.go files and nowhere else. This file orders the
// two pre-authentication stages; each auth class keeps its table in its own file:
//
//	routes_ingest.go     key-authenticated ingest endpoints with wildcard CORS
//	routes_public.go     public endpoints (health, login, OIDC)
//	routes_internal.go   HMAC-signed writer-internal endpoints
//	routes_protected.go  endpoints behind the session or API key pipeline
//
// Feature files hold handlers only. A router that matches a path or method
// returns true once it has written a response.

// serveSpecialEndpoint handles the unauthenticated endpoints that must run
// before the general CORS handling: the wildcard-CORS browser ingest endpoints
// (events, logs, analytics/collect) and the public, no-CORS endpoints (setup,
// theme manifest, analytics snippet). It returns true when it has written a
// response and the caller should stop.
func (s *Server) serveSpecialEndpoint(w http.ResponseWriter, r *http.Request) bool {
	for _, serve := range []func(http.ResponseWriter, *http.Request) bool{
		s.serveEventsIngestEndpoint,
		s.serveAlertmanagerIngestEndpoint,
		s.serveLogsIngestEndpoint,
		s.serveTelemetryIngestEndpoint,
		// Job check-ins authenticate with a project key, like the ingest endpoints.
		s.serveMonitorCheckInEndpoint,
	} {
		if serve(w, r) {
			return true
		}
	}

	// Agent key requests — public, rate-limited; before the setup page, whose
	// prefix they share.
	if s.serveKeyRequestEndpoint(w, r) {
		return true
	}

	// Setup endpoint — public, no auth required.
	if strings.HasPrefix(r.URL.Path, "/api/v1/setup/") && r.Method == http.MethodGet {
		s.serveSetup(w, r)
		return true
	}

	// Writer-internal session endpoints (CQRS): authenticated exclusively by
	// an HMAC over the request body with the shared session secret, so they
	// must bypass the cookie/API-key pipeline. 404 unless enabled.
	// The reader-to-writer API key last-use batches use the same scheme.
	if s.serveInternalEndpoint(w, r) {
		return true
	}

	// IAMBarn theme manifest — public, no auth, no redirect.
	if r.URL.Path == "/.well-known/iambarn-theme.json" && r.Method == http.MethodGet {
		s.serveThemeManifest(w, r)
		return true
	}

	// Analytics JS snippet — public, no auth required.
	if r.URL.Path == "/analytics.js" && r.Method == http.MethodGet {
		s.serveAnalyticsSnippet(w, r)
		return true
	}

	if s.serveAnalyticsCollectEndpoint(w, r) {
		return true
	}

	return false
}
