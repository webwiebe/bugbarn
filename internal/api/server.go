package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/ingesthealth"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	alertsvc "github.com/wiebe-xyz/bugbarn/internal/service/alerts"
	analyticssvc "github.com/wiebe-xyz/bugbarn/internal/service/analytics"
	issuesvc "github.com/wiebe-xyz/bugbarn/internal/service/issues"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
	projectsvc "github.com/wiebe-xyz/bugbarn/internal/service/projects"
	releasesvc "github.com/wiebe-xyz/bugbarn/internal/service/releases"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

const defaultMaxSourceMapBytes = 32 << 20 // 32 MiB

type Server struct {
	ingestHandler *ingest.Handler
	issues        *issuesvc.Service
	projects      *projectsvc.Service
	releases      *releasesvc.Service
	alerts        *alertsvc.Service
	logs          *logsvc.Service
	analytics     *analyticssvc.Service
	logger        *slog.Logger

	users              *auth.UserAuthenticator
	sessions           *auth.SessionManager
	allowedOrigins     []string // parsed from BUGBARN_ALLOWED_ORIGINS
	trustedProxies     []*net.IPNet
	logHub             *logstream.Hub
	sessionSecret      string
	publicURL          string
	maxSourceMapBytes  int64
	funnelBarnEndpoint string
	funnelBarnAPIKey   string
	selfAPIKey         string
	selfProject        string
	workerStatus       *worker.Status
	ingestHealth       func() ingesthealth.Snapshot
	autoApproveProjects bool

	loginLimiter    sync.Map // map[string]*loginAttempt
	writeForwarder  *WriteForwarder
	ingestSpool     *SpoolForwarder
	mutQueue        *mutqueue.Queue
	dbPath          string

	digestConfig    *digest.Config
	digestStore     digest.Store

	oidc *auth.OIDCClient
}

// SetMutQueue wires the mutation queue so that resolve/reopen/mute/unmute
// handlers enqueue writes instead of hitting SQLite synchronously.
func (s *Server) SetMutQueue(q *mutqueue.Queue) {
	s.mutQueue = q
}

// SetOIDCClient wires the optional iambarn OIDC login adapter. When set, the
// frontend's runtime-config reports oidc.enabled=true and the SPA redirects to
// /api/v1/oidc/login on the login screen. Local single-user login still works.
func (s *Server) SetOIDCClient(c *auth.OIDCClient) {
	s.oidc = c
}

// SetTrustedProxies sets the CIDRs from which X-Forwarded-For is trusted.
func (s *Server) SetTrustedProxies(cidrs []*net.IPNet) {
	s.trustedProxies = cidrs
}

// SetMaxSourceMapBytes sets the maximum source map upload size. Defaults to 32 MiB.
func (s *Server) SetMaxSourceMapBytes(n int64) {
	if n > 0 {
		s.maxSourceMapBytes = n
	}
}

// SetDigest wires digest configuration for the manual trigger endpoint.
func (s *Server) SetDigest(cfg digest.Config, store digest.Store) {
	s.digestConfig = &cfg
	s.digestStore = store
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

// SetFunnelBarnConfig wires optional FunnelBarn analytics tracking config.
// If endpoint is empty, the runtime-config endpoint returns enabled=false.
func (s *Server) SetFunnelBarnConfig(endpoint, apiKey string) {
	s.funnelBarnEndpoint = endpoint
	s.funnelBarnAPIKey = apiKey
}

// SetSelfReportingConfig exposes the ingest API key to the web frontend so it
// can report its own errors back into BugBarn.
func (s *Server) SetSelfReportingConfig(apiKey, project string) {
	s.selfAPIKey = apiKey
	s.selfProject = project
}

// SetWorkerStatus wires the background worker's health status into the server
// so the health endpoint can report worker health.
func (s *Server) SetWorkerStatus(ws *worker.Status) {
	s.workerStatus = ws
}

// SetIngestHealth wires the ingest-liveness monitor's snapshot into the health
// endpoint so a stalled write pipeline surfaces as a 503 — the signal that was
// missing during the 2026-06-21 outage. Provided as a function so the API layer
// stays decoupled from the monitor's lifecycle.
func (s *Server) SetIngestHealth(snapshot func() ingesthealth.Snapshot) {
	s.ingestHealth = snapshot
}

// SetAutoApproveProjects controls whether the setup endpoint auto-approves
// new projects instead of creating them with status=pending.
func (s *Server) SetAutoApproveProjects(auto bool) {
	s.autoApproveProjects = auto
}

// SetWriteForwarder configures the server to forward non-GET requests to
// an upstream writer instance. Used in reader mode (CQRS split).
func (s *Server) SetWriteForwarder(f *WriteForwarder) {
	s.writeForwarder = f
}

// SetHeldReplayer wires the held-events replayer into the projects service so
// approving a pending project drains its backlog. Writer-only.
func (s *Server) SetHeldReplayer(r projectsvc.HeldReplayer) {
	if s.projects != nil {
		s.projects.SetHeldReplayer(r)
	}
}

// SetIngestSpool wires a spool-backed forwarder for fire-and-forget ingest
// endpoints (events, logs, analytics). When set, those endpoints append to
// the on-disk spool and return 202 instead of forwarding synchronously, so
// writer downtime during deploys does not surface as 502s to SDKs.
func (s *Server) SetIngestSpool(sp *SpoolForwarder) {
	s.ingestSpool = sp
}

// SetDBPath sets the path to the SQLite database file for the backup endpoint.
func (s *Server) SetDBPath(path string) {
	s.dbPath = path
}

func NewServer(ingestHandler *ingest.Handler, store *storage.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	d := store.Domains()
	issues, releases, projects := newServiceRepos(d)
	return &Server{
		ingestHandler:     ingestHandler,
		issues:            issuesvc.New(issues, logger),
		projects:          projectsvc.New(projects, logger),
		releases:          releasesvc.New(releases, logger),
		alerts:            alertsvc.New(d.Alerts, logger),
		logs:              logsvc.New(d.Logs, logger),
		analytics:         analyticssvc.New(d.Analytics, logger),
		logger:            logger.With("component", "api"),
		maxSourceMapBytes: defaultMaxSourceMapBytes,
	}
}

func NewServerWithAuth(ingestHandler *ingest.Handler, store *storage.Store, users *auth.UserAuthenticator, sessions *auth.SessionManager, allowedOrigins []string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	d := store.Domains()
	issues, releases, projects := newServiceRepos(d)
	s := &Server{
		ingestHandler:     ingestHandler,
		issues:            issuesvc.New(issues, logger),
		projects:          projectsvc.New(projects, logger),
		releases:          releasesvc.New(releases, logger),
		alerts:            alertsvc.New(d.Alerts, logger),
		logs:              logsvc.New(d.Logs, logger),
		analytics:         analyticssvc.New(d.Analytics, logger),
		logger:            logger.With("component", "api"),
		users:             users,
		sessions:          sessions,
		allowedOrigins:    allowedOrigins,
		maxSourceMapBytes: defaultMaxSourceMapBytes,
	}
	return s
}

// Start launches background goroutines that require a context for clean
// shutdown (e.g. login-limiter cleanup). It must be called once after the
// server is fully configured and before it begins serving requests.
func (s *Server) Start(ctx context.Context) {
	go s.cleanupLoginLimiter(ctx)
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
			if s.ingestHandler != nil && !s.ingestHandler.ValidAPIKey(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if s.ingestSpool != nil {
				s.ingestSpool.Forward(w, r)
				return
			}
			if s.writeForwarder != nil {
				s.writeForwarder.Forward(w, r)
				return
			}
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
		if s.ingestHandler != nil && !s.ingestHandler.ValidAPIKey(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if s.ingestSpool != nil {
			s.ingestSpool.Forward(w, r)
			return
		}
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return
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

	// IAMBarn theme manifest — public, no auth, no redirect.
	if r.URL.Path == "/.well-known/iambarn-theme.json" && r.Method == http.MethodGet {
		s.serveThemeManifest(w, r)
		return
	}

	// Analytics JS snippet — public, no auth required.
	if r.URL.Path == "/analytics.js" && r.Method == http.MethodGet {
		s.serveAnalyticsSnippet(w, r)
		return
	}

	// Analytics collection — public, wildcard CORS for sendBeacon from any origin.
	if r.URL.Path == "/api/v1/analytics/collect" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-project")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost {
			if s.ingestSpool != nil {
				s.ingestSpool.Forward(w, r)
				return
			}
			if s.writeForwarder != nil {
				s.writeForwarder.Forward(w, r)
				return
			}
			s.collectPageView(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// OpenAPI spec and docs — public, no auth required.
	if r.URL.Path == "/api/v1/openapi.yaml" && r.Method == http.MethodGet {
		s.serveOpenAPISpec(w, r)
		return
	}
	if r.URL.Path == "/api/docs" && r.Method == http.MethodGet {
		s.serveAPIDocs(w, r)
		return
	}

	// Internal endpoint — used by reader pods for DB backup fallback.
	if r.URL.Path == "/internal/v1/db-backup" && r.Method == http.MethodGet {
		s.serveDBBackup(w, r)
		return
	}

	// Public endpoints — no authentication required.
	switch {
	case r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet:
		s.serveHealth(w, r)
		return
	case r.URL.Path == "/api/v1/runtime-config" && r.Method == http.MethodGet:
		s.serveRuntimeConfig(w, r)
		return
	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return
		}
		s.login(w, r)
		return
	case r.URL.Path == "/api/v1/logout" && r.Method == http.MethodPost:
		if s.writeForwarder != nil {
			s.writeForwarder.Forward(w, r)
			return
		}
		s.logout(w, r)
		return
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		s.me(w, r)
		return
	case r.URL.Path == "/api/v1/oidc/login" && r.Method == http.MethodGet:
		s.oidcLogin(w, r)
		return
	case r.URL.Path == "/api/v1/oidc/callback" && r.Method == http.MethodGet:
		s.oidcCallback(w, r)
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
				if scope == domain.APIKeyScopeIngest {
					// Allow ingest keys to post release markers — the setup page
					// uses the same key for both events and releases.
					if r.URL.Path == "/api/v1/releases" && r.Method == http.MethodPost {
						// fall through
					} else {
						s.logger.Warn("auth: ingest-only key rejected", "method", r.Method, "path", r.URL.Path)
						http.Error(w, "forbidden: ingest-only key cannot access this endpoint", http.StatusForbidden)
						return
					}
				}
				if scope == domain.APIKeyScopeRead {
					if r.Method != http.MethodGet {
						s.logger.Warn("auth: read-only key rejected non-GET", "method", r.Method, "path", r.URL.Path)
						http.Error(w, "forbidden: read-only key cannot modify data", http.StatusForbidden)
						return
					}
					if strings.HasPrefix(r.URL.Path, "/api/v1/settings") || strings.HasPrefix(r.URL.Path, "/api/v1/apikeys") {
						s.logger.Warn("auth: read-only key rejected settings/apikeys", "method", r.Method, "path", r.URL.Path)
						http.Error(w, "forbidden: read-only key cannot access this endpoint", http.StatusForbidden)
						return
					}
				}
				usingAPIKey = true
				apiKeyProjectID = pid
			}
		}

		if !usingSession && !usingAPIKey {
			s.logger.Warn("auth: rejected request", "method", r.Method, "path", r.URL.Path, "reason", "no session or API key")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// CSRF protection: only applies to session-authenticated state-changing requests.
		// API key requests are authenticated out-of-band, so CSRF doesn't apply.
		if usingSession && !usingAPIKey && isCSRFProtected(r) {
			if !s.validCSRF(r) {
				s.logger.Warn("csrf: rejected request", "method", r.Method, "path", r.URL.Path, "header_present", r.Header.Get("X-BugBarn-CSRF") != "")
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}

		// Refresh the CSRF cookie on every session-authenticated response when
		// the browser's copy is missing or stale, so the frontend always has a
		// valid token available for state-changing requests.
		if usingSession && s.sessions != nil {
			if sessionCookie, err := r.Cookie("bugbarn_session"); err == nil {
				if csrfCookie, err := r.Cookie("bugbarn_csrf"); err != nil || csrfCookie.Value != s.sessions.CSRFToken(sessionCookie.Value) {
					secure := secureCookie(r)
					expires := time.Now().Add(12 * time.Hour)
					http.SetCookie(w, s.sessions.CSRFCookie(sessionCookie.Value, expires, secure))
				}
			}
		}

		// Resolve project ID. X-BugBarn-Project header takes precedence for both
		// API key and session requests — it auto-creates the project if unknown,
		// so a single shared API key can route events to any project via the header.
		if slug := r.Header.Get("X-BugBarn-Project"); slug != "" {
			if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
				resolvedProjectID = proj.ID
			}
		} else if usingAPIKey && apiKeyProjectID > 0 {
			resolvedProjectID = apiKeyProjectID
		} else if usingSession && r.Method != http.MethodGet {
			if !isIssueAction(r) {
				if proj, err := s.projects.BySlug(r.Context(), "default"); err == nil {
					resolvedProjectID = proj.ID
				}
			}
		}
		if resolvedProjectID == 0 && !(usingSession && r.Method == http.MethodGet) {
			resolvedProjectID = s.projects.DefaultProjectID()
		}
		r = r.WithContext(storage.WithProjectID(r.Context(), resolvedProjectID))

		// Group-scoped filtering: when a GET carries X-BugBarn-Group and no
		// specific project was selected, resolve the group to its member IDs so that
		// storage queries use IN (...) instead of showing all projects.
		// Works for both session and API key auth.
		if r.Method == http.MethodGet && resolvedProjectID == 0 {
			if groupSlug := r.Header.Get("X-BugBarn-Group"); groupSlug != "" {
				if members, err := s.projects.ListGroupProjects(r.Context(), groupSlug); err == nil && len(members) > 0 {
					ids := make([]int64, len(members))
					for i, p := range members {
						ids[i] = p.ID
					}
					r = r.WithContext(storage.WithProjectIDs(r.Context(), ids))
				}
			}
		}
	}

	// In reader mode, forward all authenticated non-GET requests to the writer.
	if s.writeForwarder != nil && r.Method != http.MethodGet {
		s.writeForwarder.Forward(w, r)
		return
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
