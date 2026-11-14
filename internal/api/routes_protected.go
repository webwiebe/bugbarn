package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/analytics"
)

// dispatchProtected routes an authenticated request to its domain handler. The
// request is offered to each per-domain sub-dispatcher in order; the first that
// reports it handled the request wins, otherwise it's a 404.
func (s *Server) dispatchProtected(w http.ResponseWriter, r *http.Request) {
	dispatchers := []func(http.ResponseWriter, *http.Request) bool{
		s.dispatchSourceMapRoutes,
		s.dispatchSettingsReleaseRoutes,
		s.dispatchAlertRoutes,
		s.dispatchDetectionRoutes,
		s.dispatchIssueRoutes,
		s.dispatchLogEventRoutes,
		s.dispatchProjectRoutes,
		s.dispatchProjectItemRoutes,
		s.dispatchKeyRequestRoutes,
		s.dispatchMonitorRoutes,
		s.dispatchGroupAliasRoutes,
		s.dispatchAPIKeyRoutes,
		s.dispatchFacetAnalyticsRoutes,
		s.dispatchTelemetryRoutes,
		s.dispatchMiscRoutes,
	}
	for _, d := range dispatchers {
		if d(w, r) {
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) dispatchSourceMapRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/source-maps" && r.Method == http.MethodGet:
		s.listSourceMaps(w, r)
	case r.URL.Path == "/api/v1/source-maps" && r.Method == http.MethodPost:
		s.uploadSourceMap(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchSettingsReleaseRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/settings" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodPost):
		s.serveSettingsRoute(w, r)
	case r.URL.Path == "/api/v1/releases" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveReleasesRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/releases/"):
		s.serveReleaseRoute(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchAlertRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/alerts" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveAlertsRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/alerts/"):
		s.serveAlertRoute(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchIssueRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/issues" && r.Method == http.MethodGet:
		s.listIssues(w, r)
	case r.URL.Path == "/api/v1/issues/sparklines" && r.Method == http.MethodGet:
		s.issueSparklines(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/issues/"):
		if s.requireIssueInScope(w, r, issueIDFromPath(r.URL.Path)) {
			s.serveIssueRoute(w, r)
		}
	default:
		return false
	}
	return true
}

func (s *Server) dispatchLogEventRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch {
	case r.URL.Path == "/api/v1/logs":
		s.serveLogs(w, r)
	case r.URL.Path == "/api/v1/logs/histogram":
		s.serveLogsHistogram(w, r)
	case r.URL.Path == "/api/v1/logs/facets":
		s.serveLogsFacets(w, r)
	case r.URL.Path == "/api/v1/logs/stream":
		s.serveLogsStream(w, r)
	case r.URL.Path == "/api/v1/events/stream":
		s.streamEvents(w, r)
	case r.URL.Path == "/api/v1/events/timeline":
		s.serveErrorTimeline(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/events/"):
		s.getEvent(w, r)
	case r.URL.Path == "/api/v1/live/events":
		s.listRecentEvents(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchProjectRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/projects" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveProjectsRoot(w, r)
	case r.URL.Path == "/api/v1/projects/pending-count" && r.Method == http.MethodGet:
		s.servePendingProjectCount(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchProjectItemRoutes(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/projects/") {
		return false
	}
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve"):
		s.approveProject(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
		s.mergeProject(w, r)
	// Before the bare-PUT case below, which would otherwise swallow them: that one
	// matches any path under /api/v1/projects/ and treats the whole tail as a slug.
	case s.dispatchProjectSettingRoutes(w, r):
	case r.Method == http.MethodPut:
		s.renameProject(w, r)
	case r.Method == http.MethodDelete:
		s.deleteProject(w, r)
	default:
		return false
	}
	return true
}

// dispatchProjectSettingRoutes serves the per-project setting endpoints that
// hang off /api/v1/projects/:slug/.
func (s *Server) dispatchProjectSettingRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/limits"):
		s.updateProjectLimits(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/funnelbarn"):
		s.updateProjectFunnelBarnURL(w, r)
	case isSilenceWatchRoute(r):
		s.projectSilenceWatch(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchGroupAliasRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/groups" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveGroupsRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/groups/"):
		s.serveGroupRoute(w, r)
	case r.URL.Path == "/api/v1/aliases" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveAliasesRoot(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/aliases/") && r.Method == http.MethodDelete:
		s.deleteAlias(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchAPIKeyRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/apikeys" && r.Method == http.MethodGet:
		s.listAPIKeys(w, r)
	case r.URL.Path == "/api/v1/apikeys/revoke-setup" && r.Method == http.MethodPost:
		s.revokeSetupKey(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/apikeys/") && r.Method == http.MethodDelete:
		s.deleteAPIKey(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) dispatchFacetAnalyticsRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/surfaces" && r.Method == http.MethodGet:
		s.serveSurfaces(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/facets") && r.Method == http.MethodGet:
		s.serveFacetsRoute(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/analytics/") && r.Method == http.MethodGet:
		s.serveAnalyticsQuery(w, r, strings.TrimPrefix(r.URL.Path, "/api/v1/analytics/"))
	default:
		return false
	}
	return true
}

func (s *Server) dispatchMiscRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/telemetry" && r.Method == http.MethodPost:
		s.serveTelemetry(w, r)
	case r.URL.Path == "/api/v1/client-errors" && r.Method == http.MethodPost:
		s.serveClientError(w, r)
	case r.URL.Path == "/api/v1/admin/digest" && r.Method == http.MethodPost:
		s.serveDigestTrigger(w, r)
	case r.URL.Path == "/internal/v1/db-backup" && r.Method == http.MethodGet:
		s.serveDBBackup(w, r)
	default:
		return false
	}
	return true
}

const (
	telemetryHostsPath      = "/api/v1/telemetry/hosts"
	telemetryHostPathPrefix = telemetryHostsPath + "/"
)

// dispatchTelemetryRoutes serves the authenticated telemetry GET endpoints.
// The POST ingest endpoints on the same paths are handled before
// authentication by serveTelemetryIngestEndpoint.
func (s *Server) dispatchTelemetryRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch p := r.URL.Path; {
	case p == telemetrySecurityPath:
		s.searchSecurityTelemetry(w, r)
	case p == telemetryHostsPath:
		s.listTelemetryHosts(w, r)
	case strings.HasPrefix(p, telemetryHostPathPrefix):
		s.routeTelemetryHost(w, r)
	default:
		return false
	}
	return true
}

// routeTelemetryHost picks the handler for /api/v1/telemetry/hosts/{host}/{action}.
func (s *Server) routeTelemetryHost(w http.ResponseWriter, r *http.Request) {
	host, action, ok := splitHostRoute(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	var handler telemetryHostAction
	switch action {
	case "metrics":
		handler = (*Server).serveTelemetryHostMetrics
	case "series":
		handler = (*Server).serveTelemetrySeries
	}
	s.serveTelemetryHostAction(w, r, host, handler)
}

// splitHostRoute splits /api/v1/telemetry/hosts/{host}/{action}. The host
// segment is path-escaped by the client.
func splitHostRoute(escapedPath string) (host, action string, ok bool) {
	rest := strings.TrimPrefix(escapedPath, telemetryHostPathPrefix)
	seg, action, found := strings.Cut(rest, "/")
	if !found || seg == "" || strings.Contains(action, "/") {
		return "", "", false
	}
	host, err := url.PathUnescape(seg)
	if err != nil {
		return "", "", false
	}
	return host, action, true
}

const detectionRulesPath = "/api/v1/detection/rules"

func (s *Server) dispatchDetectionRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == detectionRulesPath && r.Method == http.MethodGet:
		s.listDetectionRules(w, r)
	case strings.HasPrefix(r.URL.Path, detectionRulesPath+"/"):
		s.serveDetectionRule(w, r)
	default:
		return false
	}
	return true
}

// dispatchKeyRequestRoutes serves the admin side of agent key requests: list the
// open requests and approve or deny one. Writes reach the writer through the
// generic reader forward.
func (s *Server) dispatchKeyRequestRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/key-requests" && r.Method == http.MethodGet {
		s.listKeyRequests(w, r)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/key-requests/")
	if !ok || r.Method != http.MethodPost {
		return false
	}
	id, action, found := strings.Cut(rest, "/")
	if !found || id == "" || (action != "approve" && action != "deny") {
		return false
	}
	s.resolveKeyRequest(w, r, id, action)
	return true
}

// dispatchMonitorRoutes serves the authenticated monitor routes: the list and
// the delete. Check-ins have their own key-authenticated entry point.
func (s *Server) dispatchMonitorRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/api/v1/monitors" && r.Method == http.MethodGet:
		s.listMonitors(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, monitorsPathPrefix):
		s.deleteMonitor(w, r)
	default:
		return false
	}
	return true
}

// analyticsRoutes maps the tail of /api/v1/analytics/<tail> to its query handler.
var analyticsRoutes = map[string]func(*Server, http.ResponseWriter, *http.Request, analytics.Query){
	"overview":  (*Server).analyticsOverview,
	"pages":     (*Server).analyticsPages,
	"timeline":  (*Server).analyticsTimeline,
	"referrers": (*Server).analyticsReferrers,
	"segments":  (*Server).analyticsSegments,
	"flow":      (*Server).analyticsFlow,
	"scroll":    (*Server).analyticsScroll,
	"dropout":   (*Server).analyticsDropout,
}

// serveFrontendTraces routes POST /api/v1/frontend-traces/api/v1/spans for an
// already-authenticated caller. It returns true when it wrote a response.
func (s *Server) serveFrontendTraces(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != frontendTracesPath {
		return false
	}
	if s.frontendTraces == nil {
		http.NotFound(w, r)
		return true
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	s.relayFrontendTraces(w, r)
	return true
}

const (
	// frontendTracesBase is the endpoint the SPA hands to the SpanBarn SDK. The
	// SDK appends /api/v1/spans, which is the path served here.
	frontendTracesBase = "/api/v1/frontend-traces"
	frontendTracesPath = frontendTracesBase + "/api/v1/spans"
)
