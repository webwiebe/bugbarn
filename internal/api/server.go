package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Server struct {
	ingestHandler  *ingest.Handler
	store          *storage.Store
	service        *service.Service
	users          *auth.UserAuthenticator
	sessions       *auth.SessionManager
	allowedOrigins []string // parsed from BUGBARN_ALLOWED_ORIGINS
	logHub         *logstream.Hub
	sessionSecret  string
	publicURL      string

	loginLimiter sync.Map // map[string]*loginAttempt
}

// SetLogHub wires the in-memory log streaming hub into the server.
func (s *Server) SetLogHub(h *logstream.Hub) {
	s.logHub = h
}

// SetSetupConfig wires the session secret and public URL for the setup page.
func (s *Server) SetSetupConfig(sessionSecret, publicURL string) {
	s.sessionSecret = sessionSecret
	s.publicURL = publicURL
}

func NewServer(ingestHandler *ingest.Handler, store *storage.Store) *Server {
	return &Server{ingestHandler: ingestHandler, store: store, service: service.New(store)}
}

func NewServerWithAuth(ingestHandler *ingest.Handler, store *storage.Store, users *auth.UserAuthenticator, sessions *auth.SessionManager, allowedOrigins []string) *Server {
	s := &Server{
		ingestHandler:  ingestHandler,
		store:          store,
		service:        service.New(store),
		users:          users,
		sessions:       sessions,
		allowedOrigins: allowedOrigins,
	}
	go s.cleanupLoginLimiter()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Ingest endpoint uses wildcard CORS so browser SDKs can POST from any origin
	// without credentials. The ingest-only key scope ensures read access is impossible.
	if r.URL.Path == "/api/v1/events" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key, x-bugbarn-project")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost {
			s.ingestHandler.ServeHTTP(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Log ingest endpoint — wildcard CORS, accepts ingest-scope or full-scope API keys.
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodPost {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key, x-bugbarn-project")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		// Resolve project from API key / header.
		var logProjectID int64
		if s.ingestHandler != nil {
			pid, _, ok := s.ingestHandler.APIKeyProjectScope(r)
			if ok && pid > 0 {
				logProjectID = pid
			}
		}
		if slug := r.Header.Get("X-BugBarn-Project"); slug != "" && s.store != nil {
			if proj, err := s.store.EnsureProject(r.Context(), slug); err == nil {
				logProjectID = proj.ID
			}
		}
		if logProjectID > 0 {
			r = r.WithContext(storage.WithProjectID(r.Context(), logProjectID))
		}
		s.serveLogsIngest(w, r)
		return
	}
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key, x-bugbarn-project")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Setup endpoint — public, no auth required.
	if strings.HasPrefix(r.URL.Path, "/api/v1/setup/") && r.Method == http.MethodGet {
		s.serveSetup(w, r)
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Public endpoints — no authentication required.
	switch {
	case r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"status": "ok"})
		return
	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		s.login(w, r)
		return
	case r.URL.Path == "/api/v1/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
		return
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		s.me(w, r)
		return
	}

	// All remaining endpoints require authentication. When auth is configured, the
	// request must carry either a valid session cookie or a valid API key. Full-scope
	// API keys are accepted on all protected endpoints; ingest-only keys are rejected here
	// (they may only reach the ingest endpoint handled above).
	var resolvedProjectID int64
	if s.users != nil && s.users.Enabled() {
		_, usingSession := s.sessionUser(r)
		var apiKeyProjectID int64
		usingAPIKey := false
		if s.ingestHandler != nil {
			pid, scope, ok := s.ingestHandler.APIKeyProjectScope(r)
			if ok {
				if scope == storage.APIKeyScopeIngest {
					// Ingest-only key attempted to access a protected endpoint.
					http.Error(w, "forbidden: ingest-only key cannot access this endpoint", http.StatusForbidden)
					return
				}
				usingAPIKey = true
				apiKeyProjectID = pid
			}
		}

		if !usingSession && !usingAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// CSRF protection: only applies to session-authenticated state-changing requests.
		// API key requests are authenticated out-of-band, so CSRF doesn't apply.
		if usingSession && !usingAPIKey && isCSRFProtected(r) {
			if !s.validCSRF(r) {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}

		// Resolve project ID. X-BugBarn-Project header takes precedence for both
		// API key and session requests — it auto-creates the project if unknown,
		// so a single shared API key can route events to any project via the header.
		if slug := r.Header.Get("X-BugBarn-Project"); slug != "" {
			if proj, err := s.store.EnsureProject(r.Context(), slug); err == nil {
				resolvedProjectID = proj.ID
			}
		} else if usingAPIKey && apiKeyProjectID > 0 {
			resolvedProjectID = apiKeyProjectID
		} else if usingSession && r.Method != http.MethodGet {
			// Issue mutations (resolve/reopen/mute/unmute) operate on a globally-unique
			// numeric row ID, so project context is not needed — leave projectID = 0 so
			// the storage layer skips the project_id WHERE filter.
			// All other non-GET writes default to the "default" project so that UI
			// operations like creating releases or alerts still land somewhere sensible.
			if !isIssueAction(r) {
				if proj, err := s.store.ProjectBySlug(r.Context(), "default"); err == nil {
					resolvedProjectID = proj.ID
				}
			}
		}
		// Always store the resolved project in context so storage can distinguish
		// "all projects" (0) from "no context set at all" (absent key).
		r = r.WithContext(storage.WithProjectID(r.Context(), resolvedProjectID))
	}

	// Protected route dispatch.
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
	case strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/approve") && r.Method == http.MethodPost:
		s.approveProject(w, r)
	case r.URL.Path == "/api/v1/apikeys" && r.Method == http.MethodGet:
		s.listAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/apikeys/") && r.Method == http.MethodDelete:
		s.deleteAPIKey(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/facets") && r.Method == http.MethodGet:
		s.serveFacetsRoute(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/analytics/") && r.Method == http.MethodGet:
		s.serveAnalyticsQuery(w, r)
	default:
		http.NotFound(w, r)
	}
}
