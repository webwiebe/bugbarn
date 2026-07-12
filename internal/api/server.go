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
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
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

	users               *auth.UserAuthenticator
	sessions            *auth.SessionManager
	allowedOrigins      []string // parsed from BUGBARN_ALLOWED_ORIGINS
	trustedProxies      []*net.IPNet
	logHub              *logstream.Hub
	sessionSecret       string
	publicURL           string
	maxSourceMapBytes   int64
	funnelBarnEndpoint  string
	funnelBarnAPIKey    string
	selfAPIKey          string
	selfProject         string
	workerStatus        *worker.Status
	ingestHealth        func() ingesthealth.Snapshot
	autoApproveProjects bool

	loginLimiter       sync.Map // map[string]*loginAttempt
	setupLimiter       sync.Map // map[string]*loginAttempt — per-IP limiter for the setup endpoint
	backchannelLimiter sync.Map // map[string]*loginAttempt — per-IP limiter for back-channel logout
	writeForwarder     *WriteForwarder
	ingestSpool        *SpoolForwarder
	mutQueue           *mutqueue.Queue
	dbPath             string

	digestConfig *digest.Config
	digestStore  digest.Store

	oidc *auth.OIDCClient

	// sessionStore owns the server-side web sessions behind the opaque cookie.
	// Direct (SQLite) in single-process/writer mode, Remote (writer-internal
	// HTTP) on the CQRS readers.
	sessionStore sessionstore.Store
	// oidcRefreshGrace bounds how long a session survives on stale tokens
	// while the IdP is unreachable (never applies to invalid_grant).
	oidcRefreshGrace time.Duration
	// environment ("production", "staging", …, or "" for local dev) drives
	// fail-closed defaults such as Secure cookies.
	environment string
	// internalSessionSecret enables the writer-internal /internal/v1/sessions/*
	// endpoints (HMAC over the request body with the shared session secret).
	internalSessionSecret []byte
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
	// The default Direct session store refreshes via the same OIDC client.
	if direct, ok := s.sessionStore.(*sessionstore.Direct); ok && direct != nil {
		direct.SetRefresher(c)
	}
}

// SetSessionStore overrides the session store (reader mode wires the Remote
// implementation pointing at the writer's internal endpoints).
func (s *Server) SetSessionStore(store sessionstore.Store) {
	s.sessionStore = store
	if direct, ok := store.(*sessionstore.Direct); ok && direct != nil && s.oidc != nil {
		direct.SetRefresher(s.oidc)
	}
}

// SetAuthEnvironment records the deployment environment ("production",
// "staging", "testing", or "" for local dev) so cookie/security defaults can
// fail closed outside dev.
func (s *Server) SetAuthEnvironment(env string) {
	s.environment = strings.TrimSpace(env)
}

// SetOIDCRefreshGrace bounds how long sessions are served on stale tokens
// during an IdP outage. Zero keeps the 1h default.
func (s *Server) SetOIDCRefreshGrace(d time.Duration) {
	if d > 0 {
		s.oidcRefreshGrace = d
	}
}

// SetInternalSessionSecret enables the writer-internal session endpoints,
// authenticated by an HMAC over the request body with this shared secret.
// Writer/single-process only.
func (s *Server) SetInternalSessionSecret(secret string) {
	secret = strings.TrimSpace(secret)
	if secret != "" {
		s.internalSessionSecret = []byte(secret)
	}
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
		sessionStore:      sessionstore.NewDirect(store, nil),
		oidcRefreshGrace:  time.Hour,
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
	// Unauthenticated wildcard-CORS ingest + public no-CORS endpoints run first.
	if s.serveSpecialEndpoint(w, r) {
		return
	}

	s.setCORSHeaders(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Public endpoints that use the general CORS headers.
	if s.servePublicEndpoint(w, r) {
		return
	}

	// Authenticate, enforce key scope/CSRF, and resolve project/group scope.
	var ok bool
	r, ok = s.authenticateAndResolve(w, r)
	if !ok {
		return
	}

	// In reader mode, forward all authenticated non-GET requests to the writer.
	if s.writeForwarder != nil && r.Method != http.MethodGet {
		s.writeForwarder.Forward(w, r)
		return
	}

	s.dispatchProtected(w, r)
}
