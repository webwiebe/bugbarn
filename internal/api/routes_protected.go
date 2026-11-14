package api

import "net/http"

const (
	// frontendTracesBase is the endpoint the SPA hands to the SpanBarn SDK. The
	// SDK appends /api/v1/spans, which is the path served here.
	frontendTracesBase = "/api/v1/frontend-traces"
	frontendTracesPath = frontendTracesBase + "/api/v1/spans"

	telemetryHostsPath = "/api/v1/telemetry/hosts"
	detectionRulesPath = "/api/v1/detection/rules"
)

// registerProtectedRoutes registers the endpoints behind the session or API key
// pipeline: general CORS, authentication with scope, CSRF and project
// resolution, then (in a reader) the hand-off of every write to the writer.
// The "/" pattern at the end sits behind the same pipeline, so a request that
// matches nothing is 401 until authenticated and then 404 or 405.
func (s *Server) registerProtectedRoutes(rt *router) {
	prot := rt.group(s.generalCORS, s.authenticate, s.forwardWrites)
	// Read-scope API keys never reach these, even for a GET.
	admin := rt.group(s.generalCORS, withRouteFlag(flagAdminOnly), s.authenticate, s.forwardWrites)
	// Frontend traces keep no BugBarn state, so a reader serves them itself, and
	// the SPA posts them without the CSRF header.
	traces := rt.group(s.generalCORS, withRouteFlag(flagNoCSRF), s.authenticate)

	s.registerIssueRoutes(prot)
	s.registerProjectRoutes(prot)
	s.registerAlertRoutes(prot)
	s.registerLogAndAnalyticsRoutes(prot)
	s.registerTelemetryRoutes(prot)
	s.registerAdminRoutes(admin)
	s.registerCatalogRoutes(prot)

	prot.handle("POST /api/v1/telemetry", s.serveTelemetry)
	prot.handle("POST /api/v1/client-errors", s.serveClientError)
	prot.handle("POST /api/v1/admin/digest", s.serveDigestTrigger)
	prot.handle("GET /internal/v1/db-backup", s.serveDBBackup)

	traces.handle("POST "+frontendTracesPath, s.serveFrontendTraces)
	prot.fallback("/", s.unmatched)
}

// registerAdminRoutes registers the settings and API key endpoints, which a
// read-scope key never reaches, even for a GET.
func (s *Server) registerAdminRoutes(g group) {
	g.handle("GET /api/v1/settings", s.getSettings)
	g.handle("PUT /api/v1/settings", s.updateSettings)
	g.handle("POST /api/v1/settings", s.updateSettings)
	g.handle("GET /api/v1/apikeys", s.listAPIKeys)
	g.handle("POST /api/v1/apikeys/revoke-setup", s.revokeSetupKey)
	g.handle("DELETE /api/v1/apikeys/{id}", s.deleteAPIKey)
}

// registerCatalogRoutes registers source maps, releases, key requests, monitors,
// groups, aliases and facets.
func (s *Server) registerCatalogRoutes(prot group) {
	prot.handle("GET /api/v1/source-maps", s.listSourceMaps)
	prot.handle("POST /api/v1/source-maps", s.uploadSourceMap)

	prot.handle("GET /api/v1/releases", s.listReleases)
	prot.handle("POST /api/v1/releases", s.createRelease)
	prot.handle("GET /api/v1/releases/{id}", s.getRelease)
	prot.handle("PUT /api/v1/releases/{id}", s.updateRelease)
	prot.handle("DELETE /api/v1/releases/{id}", s.deleteRelease)

	// The admin side of agent key requests: list the open requests and decide one.
	prot.handle("GET /api/v1/key-requests", s.listKeyRequests)
	prot.handle("POST /api/v1/key-requests/{id}/approve", s.approveKeyRequest)
	prot.handle("POST /api/v1/key-requests/{id}/deny", s.denyKeyRequest)

	prot.handle("GET /api/v1/monitors", s.listMonitors)
	prot.handle("DELETE /api/v1/monitors/{slug}", s.deleteMonitor)

	prot.handle("GET /api/v1/groups", s.listGroups)
	prot.handle("POST /api/v1/groups", s.createGroup)
	prot.handle("GET /api/v1/groups/{slug}/projects", s.listGroupProjects)
	prot.handle("POST /api/v1/groups/{slug}/projects", s.assignProjectToGroup)
	prot.handle("DELETE /api/v1/groups/{slug}/projects/{project}", s.removeProjectFromGroup)
	prot.handle("DELETE /api/v1/groups/{slug}", s.deleteGroup)

	prot.handle("GET /api/v1/aliases", s.listAliases)
	prot.handle("POST /api/v1/aliases", s.createAlias)
	prot.handle("DELETE /api/v1/aliases/{slug}", s.deleteAlias)

	prot.handle("GET /api/v1/surfaces", s.serveSurfaces)
	prot.handle("GET /api/v1/facets", s.listFacetKeys)
	prot.fallback("GET /api/v1/facets/{$}", s.listFacetKeys)
	prot.handle("GET /api/v1/facets/{key}", s.listFacetValues)
}

func (s *Server) registerIssueRoutes(g group) {
	g.handle("GET /api/v1/issues", s.listIssues)
	g.handle("GET /api/v1/issues/sparklines", s.issueSparklines)
	// An issue of another project answers 404 to a caller confined to one.
	g.handle("GET /api/v1/issues/{id}", s.inIssueScope(s.getIssue))
	g.handle("POST /api/v1/issues/{id}/resolve", s.inIssueScope(s.resolveIssue))
	g.handle("POST /api/v1/issues/{id}/reopen", s.inIssueScope(s.reopenIssue))
	g.handle("PATCH /api/v1/issues/{id}/mute", s.inIssueScope(s.muteIssue))
	g.handle("PATCH /api/v1/issues/{id}/unmute", s.inIssueScope(s.unmuteIssue))
	g.handle("GET /api/v1/issues/{id}/events", s.inIssueScope(s.listIssueEvents))
	g.handle("GET /api/v1/issues/{id}/releases", s.inIssueScope(s.listIssueReleases))
}

func (s *Server) registerProjectRoutes(g group) {
	g.handle("GET /api/v1/projects", s.listProjects)
	g.handle("POST /api/v1/projects", s.createProject)
	g.handle("GET /api/v1/projects/pending-count", s.servePendingProjectCount)
	g.handle("POST /api/v1/projects/{slug}/approve", s.approveProject)
	g.handle("POST /api/v1/projects/{slug}/merge", s.mergeProject)
	g.handle("PUT /api/v1/projects/{slug}/limits", s.updateProjectLimits)
	g.handle("PUT /api/v1/projects/{slug}/funnelbarn", s.updateProjectFunnelBarnURL)
	g.handle("GET /api/v1/projects/{slug}/silence-watch", s.getSilenceWatch)
	g.handle("PUT /api/v1/projects/{slug}/silence-watch", s.putSilenceWatch)
	g.handle("PUT /api/v1/projects/{slug}", s.renameProject)
	g.handle("DELETE /api/v1/projects/{slug}", s.deleteProject)
	g.fallback("DELETE /api/v1/projects/{slug}/{$}", s.deleteProject)
}

func (s *Server) registerAlertRoutes(g group) {
	g.handle("GET /api/v1/alerts", s.listAlerts)
	g.handle("POST /api/v1/alerts", s.createAlert)
	g.handle("GET /api/v1/alerts/{id}", s.getAlert)
	g.handle("PUT /api/v1/alerts/{id}", s.updateAlert)
	g.handle("DELETE /api/v1/alerts/{id}", s.deleteAlert)

	g.handle("GET "+detectionRulesPath, s.listDetectionRules)
	g.handle("PUT "+detectionRulesPath+"/{id}", s.putDetectionRule)
	g.handle("DELETE "+detectionRulesPath+"/{id}", s.deleteDetectionRule)
}

func (s *Server) registerLogAndAnalyticsRoutes(g group) {
	g.handle("GET /api/v1/logs", s.serveLogs)
	g.handle("GET /api/v1/logs/histogram", s.serveLogsHistogram)
	g.handle("GET /api/v1/logs/facets", s.serveLogsFacets)
	g.handle("GET /api/v1/logs/stream", s.serveLogsStream)
	g.handle("GET /api/v1/events/stream", s.streamEvents)
	g.handle("GET /api/v1/events/timeline", s.serveErrorTimeline)
	g.handle("GET /api/v1/events/{id}", s.getEvent)
	g.handle("GET /api/v1/live/events", s.listRecentEvents)

	g.handle("GET /api/v1/analytics/overview", s.analyticsView((*Server).analyticsOverview))
	g.handle("GET /api/v1/analytics/pages", s.analyticsView((*Server).analyticsPages))
	g.handle("GET /api/v1/analytics/timeline", s.analyticsView((*Server).analyticsTimeline))
	g.handle("GET /api/v1/analytics/referrers", s.analyticsView((*Server).analyticsReferrers))
	g.handle("GET /api/v1/analytics/segments", s.analyticsView((*Server).analyticsSegments))
	g.handle("GET /api/v1/analytics/flow", s.analyticsView((*Server).analyticsFlow))
	g.handle("GET /api/v1/analytics/scroll", s.analyticsView((*Server).analyticsScroll))
	g.handle("GET /api/v1/analytics/dropout", s.analyticsView((*Server).analyticsDropout))
}

// registerTelemetryRoutes registers the authenticated telemetry reads. The POST
// ingest endpoints on the same paths are key-authenticated (routes_ingest.go).
func (s *Server) registerTelemetryRoutes(g group) {
	g.handle("GET "+telemetrySecurityPath, s.searchSecurityTelemetry)
	g.handle("GET "+telemetryHostsPath, s.listTelemetryHosts)
	g.handle("GET "+telemetryHostsPath+"/{host}/metrics", s.hostAction((*Server).serveTelemetryHostMetrics))
	g.handle("GET "+telemetryHostsPath+"/{host}/series", s.hostAction((*Server).serveTelemetrySeries))
}

// serveFrontendTraces relays one browser span batch for an authenticated
// caller, 404 while no SpanBarn target is configured.
func (s *Server) serveFrontendTraces(w http.ResponseWriter, r *http.Request) {
	if s.frontendTraces == nil {
		http.NotFound(w, r)
		return
	}
	s.relayFrontendTraces(w, r)
}
