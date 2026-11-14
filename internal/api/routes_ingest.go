package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// The routes below authenticate with a project API key (or nothing, for page
// view collection) and run before the general CORS handling.

// serveAlertmanagerIngestEndpoint accepts Alertmanager's batch webhook format.
func (s *Server) serveAlertmanagerIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/alertmanager" {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if !s.requireIngestKey(w, r) {
		return true
	}
	s.deliverWrite(w, r, s.ingestHandler.ServeAlertmanagerHTTP)
	return true
}

// serveEventsIngestEndpoint handles /api/v1/events with wildcard CORS so browser
// SDKs can POST from any origin without credentials. The ingest-only key scope
// ensures read access is impossible.
func (s *Server) serveEventsIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/events" {
		return false
	}
	setWildcardCORS(w, "POST, OPTIONS", ingestCORSHeaders)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if r.Method == http.MethodPost {
		if !s.requireIngestKey(w, r) {
			return true
		}
		s.deliverWrite(w, r, s.ingestHandler.ServeHTTP)
		return true
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return true
}

// serveLogsIngestEndpoint handles POST/OPTIONS /api/v1/logs — wildcard CORS,
// accepts ingest-scope or full-scope API keys.
func (s *Server) serveLogsIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodPost {
		s.handleLogsIngestPost(w, r)
		return true
	}
	if r.URL.Path == "/api/v1/logs" && r.Method == http.MethodOptions {
		setWildcardCORS(w, "POST, OPTIONS", ingestCORSHeaders)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// serveAnalyticsCollectEndpoint handles /api/v1/analytics/collect — public,
// wildcard CORS for sendBeacon from any origin.
func (s *Server) serveAnalyticsCollectEndpoint(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/analytics/collect" {
		return false
	}
	setWildcardCORS(w, "POST, OPTIONS", analyticsCORSHeaders)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if r.Method == http.MethodPost {
		s.deliverWrite(w, r, s.collectPageView)
		return true
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return true
}

const (
	monitorsPathPrefix = "/api/v1/monitors/"
	checkInsSuffix     = "/check-ins"
)

// serveMonitorCheckInEndpoint routes POST /api/v1/monitors/<slug>/check-ins.
// Job check-ins authenticate with a project key, like the ingest endpoints.
func (s *Server) serveMonitorCheckInEndpoint(w http.ResponseWriter, r *http.Request) bool {
	slug, ok := monitorCheckInSlug(r.URL.Path)
	if !ok {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	s.handleMonitorCheckIn(w, r, slug)
	return true
}

// monitorCheckInSlug extracts <slug> from /api/v1/monitors/<slug>/check-ins.
func monitorCheckInSlug(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, monitorsPathPrefix)
	if !ok {
		return "", false
	}
	slug, ok := strings.CutSuffix(rest, checkInsSuffix)
	if !ok || slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

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

// serveTelemetryIngestEndpoint routes POST /api/v1/telemetry/{security,metrics}
// and answers 405 to methods other than GET and POST. GET on the same paths is
// the authenticated read API (dispatchTelemetryRoutes). No CORS: the senders
// are Vector agents, never browsers.
func (s *Server) serveTelemetryIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	kind := telemetryKind(r.URL.Path)
	if kind == "" || r.Method == http.MethodGet {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	s.handleTelemetryIngest(w, r, kind)
	return true
}
