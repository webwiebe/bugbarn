package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

const (
	telemetrySecurityPath = "/api/v1/telemetry/security"
	telemetryMetricsPath  = "/api/v1/telemetry/metrics"
)

// telemetryKind maps a telemetry ingest path to its queue kind, or "" for any
// other path.
func telemetryKind(path string) string {
	switch path {
	case telemetrySecurityPath:
		return queue.KindSecurity
	case telemetryMetricsPath:
		return queue.KindMetrics
	}
	return ""
}

// registerIngestRoutes registers the endpoints that authenticate with a project
// API key (or nothing, for page view collection). They answer ahead of the
// general CORS handling and the session pipeline.
func (s *Server) registerIngestRoutes(rt *router) {
	// Browser SDKs post from any origin without credentials; the ingest-only key
	// scope makes read access impossible. A wrong method answers 405 without an
	// Allow header, as it always did on these two.
	events := rt.group(wildcardCORS("POST, OPTIONS", ingestCORSHeaders))
	events.fallback("OPTIONS /api/v1/events", preflight)
	events.handle("POST /api/v1/events", s.postEvent)
	events.fallback("/api/v1/events", methodNotAllowed(""))

	// GET /api/v1/logs is the authenticated explorer (routes_protected.go).
	logs := rt.group(wildcardCORS("POST, OPTIONS", ingestCORSHeaders))
	logs.fallback("OPTIONS /api/v1/logs", preflight)
	logs.handle("POST /api/v1/logs", s.handleLogsIngestPost)

	collect := rt.group(wildcardCORS("POST, OPTIONS", analyticsCORSHeaders))
	collect.fallback("OPTIONS /api/v1/analytics/collect", preflight)
	collect.handle("POST /api/v1/analytics/collect", s.postPageView)
	collect.fallback("/api/v1/analytics/collect", methodNotAllowed(""))

	bare := rt.group()
	bare.handle("POST /api/v1/alertmanager", s.postAlertmanager)
	bare.fallback("/api/v1/alertmanager", methodNotAllowed(http.MethodPost))

	// Job check-ins authenticate with a project key, like the ingest endpoints.
	bare.handle("POST /api/v1/monitors/{slug}/check-ins", s.handleMonitorCheckIn)
	bare.fallback("/api/v1/monitors/{slug}/check-ins", methodNotAllowed(http.MethodPost))

	// The senders are Vector agents, never browsers, so no CORS. GET on the same
	// paths is the authenticated read API (routes_protected.go).
	bare.handle("POST "+telemetrySecurityPath, s.ingestTelemetry(queue.KindSecurity))
	bare.handle("POST "+telemetryMetricsPath, s.ingestTelemetry(queue.KindMetrics))
	for _, path := range []string{telemetrySecurityPath, telemetryMetricsPath} {
		rejectMethods(bare, path, "GET, POST", "HEAD", "PUT", "PATCH", "DELETE", "OPTIONS")
	}
}

func (s *Server) postEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requireIngestKey(w, r) {
		return
	}
	s.deliverWrite(w, r, s.ingestHandler.ServeHTTP)
}

func (s *Server) postPageView(w http.ResponseWriter, r *http.Request) {
	s.deliverWrite(w, r, s.collectPageView)
}

// postAlertmanager accepts Alertmanager's batch webhook format.
func (s *Server) postAlertmanager(w http.ResponseWriter, r *http.Request) {
	if !s.requireIngestKey(w, r) {
		return
	}
	s.deliverWrite(w, r, s.ingestHandler.ServeAlertmanagerHTTP)
}

func (s *Server) ingestTelemetry(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { s.handleTelemetryIngest(w, r, kind) }
}
