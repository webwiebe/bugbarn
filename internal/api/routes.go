package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// serveSpecialEndpoint handles the unauthenticated endpoints that must run
// before the general CORS handling: the wildcard-CORS browser ingest endpoints
// (events, logs, analytics/collect) and the public, no-CORS endpoints (setup,
// theme manifest, analytics snippet). It returns true when it has written a
// response and the caller should stop.
//
//nolint:gocognit,gocyclo,funlen // sequential special-endpoint handlers with per-endpoint CORS/forwarding.
func (s *Server) serveSpecialEndpoint(w http.ResponseWriter, r *http.Request) bool {
	// Ingest endpoint uses wildcard CORS so browser SDKs can POST from any origin
	// without credentials. The ingest-only key scope ensures read access is impossible.
	if r.URL.Path == "/api/v1/events" {
		setWildcardCORS(w, "POST, OPTIONS", ingestCORSHeaders)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		if r.Method == http.MethodPost {
			if s.ingestHandler != nil && !s.ingestHandler.ValidAPIKey(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return true
			}
			if s.ingestSpool != nil {
				s.ingestSpool.Forward(w, r)
				return true
			}
			if s.writeForwarder != nil {
				s.writeForwarder.Forward(w, r)
				return true
			}
			s.ingestHandler.ServeHTTP(w, r)
			return true
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}

	// Log ingest endpoint — wildcard CORS, accepts ingest-scope or full-scope API keys.
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodPost {
		setWildcardCORS(w, "POST, OPTIONS", ingestCORSHeaders)
		if s.ingestHandler != nil && !s.ingestHandler.ValidAPIKey(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return true
		}
		if s.ingestSpool != nil {
			s.ingestSpool.Forward(w, r)
			return true
		}
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return true
		}
		// Resolve project from API key / header.
		var logProjectID int64
		if s.ingestHandler != nil {
			pid, _, ok := s.ingestHandler.APIKeyProjectScope(r)
			if ok && pid > 0 {
				logProjectID = pid
			}
		}
		if slug := r.Header.Get("X-BugBarn-Project"); slug != "" && s.projects != nil {
			if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
				logProjectID = proj.ID
			}
		}
		if logProjectID > 0 {
			r = r.WithContext(storage.WithProjectID(r.Context(), logProjectID))
		}
		s.serveLogsIngest(w, r)
		return true
	}
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodOptions {
		setWildcardCORS(w, "POST, OPTIONS", ingestCORSHeaders)
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// Setup endpoint — public, no auth required.
	if strings.HasPrefix(r.URL.Path, "/api/v1/setup/") && r.Method == http.MethodGet {
		s.serveSetup(w, r)
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

	// Analytics collection — public, wildcard CORS for sendBeacon from any origin.
	if r.URL.Path == "/api/v1/analytics/collect" {
		setWildcardCORS(w, "POST, OPTIONS", analyticsCORSHeaders)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		if r.Method == http.MethodPost {
			if s.ingestSpool != nil {
				s.ingestSpool.Forward(w, r)
				return true
			}
			if s.writeForwarder != nil {
				s.writeForwarder.Forward(w, r)
				return true
			}
			s.collectPageView(w, r)
			return true
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}

	return false
}

// servePublicEndpoint handles the endpoints that require no authentication but
// do use the general CORS headers (already set by the caller). It returns true
// when it has written a response and the caller should stop.
//
//nolint:gocyclo // flat public-route table mapping path+method to a handler.
func (s *Server) servePublicEndpoint(w http.ResponseWriter, r *http.Request) bool {
	// OpenAPI spec and docs — public, no auth required.
	if r.URL.Path == "/api/v1/openapi.yaml" && r.Method == http.MethodGet {
		s.serveOpenAPISpec(w, r)
		return true
	}
	if r.URL.Path == "/api/docs" && r.Method == http.MethodGet {
		s.serveAPIDocs(w, r)
		return true
	}

	// Internal endpoint — used by reader pods for DB backup fallback.
	if r.URL.Path == "/internal/v1/db-backup" && r.Method == http.MethodGet {
		s.serveDBBackup(w, r)
		return true
	}

	// Public endpoints — no authentication required.
	switch {
	case r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet:
		s.serveHealth(w, r)
		return true
	case r.URL.Path == "/api/v1/runtime-config" && r.Method == http.MethodGet:
		s.serveRuntimeConfig(w, r)
		return true
	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return true
		}
		s.login(w, r)
		return true
	case r.URL.Path == "/api/v1/logout" && r.Method == http.MethodPost:
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return true
		}
		s.logout(w, r)
		return true
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		s.me(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/login" && r.Method == http.MethodGet:
		s.oidcLogin(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/callback" && r.Method == http.MethodGet:
		s.oidcCallback(w, r)
		return true
	}

	return false
}

// dispatchProtected routes an authenticated request to its domain handler.
//
//nolint:gocyclo,gocognit,funlen // flat route table mapping path+method to a handler.
func (s *Server) dispatchProtected(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/source-maps" && r.Method == http.MethodGet:
		s.listSourceMaps(w, r)
	case r.URL.Path == "/api/v1/source-maps" && r.Method == http.MethodPost:
		s.uploadSourceMap(w, r)
	case r.URL.Path == "/api/v1/settings" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodPost):
		s.serveSettingsRoute(w, r)
	case r.URL.Path == "/api/v1/releases" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveReleasesRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/releases/"):
		s.serveReleaseRoute(w, r)
	case r.URL.Path == "/api/v1/alerts" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveAlertsRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/alerts/"):
		s.serveAlertRoute(w, r)
	case r.URL.Path == "/api/v1/issues" && r.Method == http.MethodGet:
		s.listIssues(w, r)
	case r.URL.Path == "/api/v1/issues/sparklines" && r.Method == http.MethodGet:
		s.issueSparklines(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/issues/"):
		s.serveIssueRoute(w, r)
	case r.URL.Path == "/api/v1/logs" && r.Method == http.MethodGet:
		s.serveLogs(w, r)
	case r.URL.Path == "/api/v1/logs/stream" && r.Method == http.MethodGet:
		s.serveLogsStream(w, r)
	case r.URL.Path == "/api/v1/events/stream" && r.Method == http.MethodGet:
		s.streamEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/events/") && r.Method == http.MethodGet:
		s.getEvent(w, r)
	case r.URL.Path == "/api/v1/live/events" && r.Method == http.MethodGet:
		s.listRecentEvents(w, r)
	case r.URL.Path == "/api/v1/projects" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveProjectsRoot(w, r)
	case r.URL.Path == "/api/v1/projects/pending-count" && r.Method == http.MethodGet:
		s.servePendingProjectCount(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/approve") && r.Method == http.MethodPost:
		s.approveProject(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/merge") && r.Method == http.MethodPost:
		s.mergeProject(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && r.Method == http.MethodPut:
		s.renameProject(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && r.Method == http.MethodDelete:
		s.deleteProject(w, r)
	case r.URL.Path == "/api/v1/groups" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveGroupsRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/groups/"):
		s.serveGroupRoute(w, r)
	case r.URL.Path == "/api/v1/aliases" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveAliasesRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/aliases/") && r.Method == http.MethodDelete:
		s.deleteAlias(w, r)
	case r.URL.Path == "/api/v1/apikeys" && r.Method == http.MethodGet:
		s.listAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/apikeys/") && r.Method == http.MethodDelete:
		s.deleteAPIKey(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/facets") && r.Method == http.MethodGet:
		s.serveFacetsRoute(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/analytics/") && r.Method == http.MethodGet:
		s.serveAnalyticsQuery(w, r)
	case r.URL.Path == "/api/v1/telemetry" && r.Method == http.MethodPost:
		s.serveTelemetry(w, r)
	case r.URL.Path == "/api/v1/client-errors" && r.Method == http.MethodPost:
		s.serveClientError(w, r)
	case r.URL.Path == "/api/v1/admin/digest" && r.Method == http.MethodPost:
		s.serveDigestTrigger(w, r)
	default:
		http.NotFound(w, r)
	}
}
