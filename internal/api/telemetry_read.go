package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/service/telemetryview"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

const (
	telemetryHostsPath      = "/api/v1/telemetry/hosts"
	telemetryHostPathPrefix = telemetryHostsPath + "/"
)

// SetTelemetryViews wires the telemetry read endpoints. The writer and the
// monolith pass telemetrydb.Fixed over the handles they write through; reader
// pods pass telemetrydb.Lazy sources.
func (s *Server) SetTelemetryViews(security, metrics telemetrydb.Source) {
	s.telemetryView = telemetryview.New(security, metrics, s.logger)
}

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
		s.serveTelemetryHostRoute(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) telemetryViewOrUnavailable(w http.ResponseWriter) *telemetryview.Service {
	if s.telemetryView == nil {
		http.Error(w, "telemetry is not configured on this server", http.StatusServiceUnavailable)
	}
	return s.telemetryView
}

type securityRowJSON struct {
	ID      int64     `json:"id"`
	TS      time.Time `json:"ts"`
	Source  string    `json:"source"`
	Host    string    `json:"host"`
	Kind    string    `json:"kind"`
	SrcIP   string    `json:"src_ip"`
	User    string    `json:"user"`
	Action  string    `json:"action"`
	Status  int       `json:"status"`
	Method  string    `json:"method"`
	Path    string    `json:"path"`
	Message string    `json:"message"`
	Raw     string    `json:"raw"`
}

// searchSecurityTelemetry handles GET /api/v1/telemetry/security.
func (s *Server) searchSecurityTelemetry(w http.ResponseWriter, r *http.Request) {
	view := s.telemetryViewOrUnavailable(w)
	if view == nil {
		return
	}
	q, err := parseSecurityQuery(r.URL.Query())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	rows, err := view.SearchSecurity(r.Context(), q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	out := make([]securityRowJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, securityRowJSON{
			ID: row.ID, TS: row.TS, Source: row.Source, Host: row.Host, Kind: row.Kind,
			SrcIP: row.SrcIP, User: row.User, Action: row.Action, Status: row.Status,
			Method: row.Method, Path: row.Path, Message: row.Message, Raw: row.Raw,
		})
	}
	limit := q.Limit
	if limit == 0 {
		limit = telemetryview.DefaultSecurityLimit
	}
	var next *string
	if len(rows) == limit {
		c := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		next = &c
	}
	writeJSON(w, map[string]any{"rows": out, "next_cursor": next})
}

func parseSecurityQuery(v url.Values) (telemetrydb.SecurityQuery, error) {
	q := telemetrydb.SecurityQuery{
		Source: strings.TrimSpace(v.Get("source")),
		Host:   strings.TrimSpace(v.Get("host")),
		Kind:   strings.TrimSpace(v.Get("kind")),
		SrcIP:  strings.TrimSpace(v.Get("src_ip")),
		User:   strings.TrimSpace(v.Get("user")),
		Text:   strings.TrimSpace(v.Get("q")),
	}
	var err error
	if q.From, q.To, err = parseTimeRange(v); err != nil {
		return q, err
	}
	if q.Status, err = parseIntParam(v, "status"); err != nil {
		return q, err
	}
	if q.Limit, err = parseIntParam(v, "limit"); err != nil {
		return q, err
	}
	cursor, err := parseIntParam(v, "cursor")
	if err != nil || cursor < 0 {
		return q, apperr.InvalidInput("cursor must be a positive integer", err)
	}
	q.Before = int64(cursor)
	return q, nil
}

// listTelemetryHosts handles GET /api/v1/telemetry/hosts.
func (s *Server) listTelemetryHosts(w http.ResponseWriter, r *http.Request) {
	view := s.telemetryViewOrUnavailable(w)
	if view == nil {
		return
	}
	hosts, err := view.Hosts(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if hosts == nil {
		hosts = []telemetrydb.Host{}
	}
	writeJSON(w, map[string]any{"hosts": hosts})
}

// serveTelemetryHostRoute handles GET /api/v1/telemetry/hosts/{host}/metrics
// and /api/v1/telemetry/hosts/{host}/series.
func (s *Server) serveTelemetryHostRoute(w http.ResponseWriter, r *http.Request) {
	host, action, ok := splitHostRoute(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	view := s.telemetryViewOrUnavailable(w)
	if view == nil {
		return
	}
	switch action {
	case "metrics":
		names, err := view.HostMetrics(r.Context(), host)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if names == nil {
			names = []string{}
		}
		writeJSON(w, map[string]any{"host": host, "metrics": names})
	case "series":
		s.serveTelemetrySeries(w, r, view, host)
	default:
		http.NotFound(w, r)
	}
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

func (s *Server) serveTelemetrySeries(w http.ResponseWriter, r *http.Request, view *telemetryview.Service, host string) {
	v := r.URL.Query()
	metric := strings.TrimSpace(v.Get("metric"))
	from, to, err := parseTimeRange(v)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	series, err := view.Series(r.Context(), host, metric, from, to)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resolution := "1m"
	if series.Hourly {
		resolution = "1h"
	}
	points := series.Points
	if points == nil {
		points = []telemetrydb.Point{}
	}
	writeJSON(w, map[string]any{"host": host, "metric": metric, "resolution": resolution, "points": points})
}

// parseTimeRange reads from/to as RFC 3339 or unix milliseconds. A missing
// value stays zero; the service fills in defaults.
func parseTimeRange(v url.Values) (time.Time, time.Time, error) {
	from, err := parseTimeParam(v, "from")
	if err != nil {
		return from, time.Time{}, err
	}
	to, err := parseTimeParam(v, "to")
	return from, to, err
}

func parseTimeParam(v url.Values, name string) (time.Time, error) {
	raw := strings.TrimSpace(v.Get(name))
	if raw == "" {
		return time.Time{}, nil
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, apperr.InvalidInput(name+" must be RFC 3339 or unix milliseconds", err)
	}
	return t.UTC(), nil
}

func parseIntParam(v url.Values, name string) (int, error) {
	raw := strings.TrimSpace(v.Get(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.InvalidInput(name+" must be an integer", err)
	}
	return n, nil
}
