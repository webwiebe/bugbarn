package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// loginAttempt tracks the rate-limit window for a single IP.
type loginAttempt struct {
	count       int
	windowStart time.Time
}

const (
	loginRateLimit   = 10              // max attempts per window
	loginRateWindow  = time.Minute     // window duration
	loginCleanupFreq = 5 * time.Minute // how often to purge stale entries
)

type Server struct {
	ingestHandler  *ingest.Handler
	store          *storage.Store
	service        *service.Service
	users          *auth.UserAuthenticator
	sessions       *auth.SessionManager
	allowedOrigins []string // parsed from BUGBARN_ALLOWED_ORIGINS

	loginLimiter sync.Map // map[string]*loginAttempt
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

// cleanupLoginLimiter periodically removes stale IP entries from the login limiter.
func (s *Server) cleanupLoginLimiter() {
	ticker := time.NewTicker(loginCleanupFreq)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-loginRateWindow)
		s.loginLimiter.Range(func(key, value any) bool {
			if a, ok := value.(*loginAttempt); ok && a.windowStart.Before(cutoff) {
				s.loginLimiter.Delete(key)
			}
			return true
		})
	}
}

type route struct {
	method  string
	path    string
	prefix  bool
	handler http.HandlerFunc
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.setCORSHeaders(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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

	// Ingest endpoint — API key authentication only (handled by the ingest handler itself).
	if r.URL.Path == "/api/v1/events" && r.Method == http.MethodPost {
		s.ingestHandler.ServeHTTP(w, r)
		return
	}

	// All remaining endpoints require authentication. When auth is configured, the
	// request must carry either a valid session cookie or a valid API key. API keys
	// are accepted on all protected endpoints so that CI/CD scripts can call the
	// releases, source-maps, and other management endpoints without a browser session.
	var resolvedProjectID int64
	if s.users != nil && s.users.Enabled() {
		_, usingSession := s.sessionUser(r)
		var apiKeyProjectID int64
		usingAPIKey := false
		if s.ingestHandler != nil {
			pid, ok := s.ingestHandler.APIKeyProject(r)
			if ok {
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

		// Resolve project ID: API key binding takes precedence.
		if usingAPIKey && apiKeyProjectID > 0 {
			resolvedProjectID = apiKeyProjectID
		} else if usingSession {
			slug := r.Header.Get("X-BugBarn-Project")
			if slug == "" {
				slug = "default"
			}
			if proj, err := s.store.ProjectBySlug(r.Context(), slug); err == nil {
				resolvedProjectID = proj.ID
			}
		}
	}
	if resolvedProjectID > 0 {
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
	case r.URL.Path == "/api/v1/events/stream" && r.Method == http.MethodGet:
		s.streamEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/events/") && r.Method == http.MethodGet:
		s.getEvent(w, r)
	case r.URL.Path == "/api/v1/live/events" && r.Method == http.MethodGet:
		s.listRecentEvents(w, r)
	case r.URL.Path == "/api/v1/projects" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.serveProjectsRoot(w, r)
	case r.URL.Path == "/api/v1/apikeys" && r.Method == http.MethodGet:
		s.listAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/apikeys/") && r.Method == http.MethodDelete:
		s.deleteAPIKey(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/facets") && r.Method == http.MethodGet:
		s.serveFacetsRoute(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.users == nil || !s.users.Enabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}

	// Rate-limit by client IP.
	ip := clientIP(r)
	now := time.Now()
	val, _ := s.loginLimiter.LoadOrStore(ip, &loginAttempt{windowStart: now})
	attempt := val.(*loginAttempt)
	if now.Sub(attempt.windowStart) >= loginRateWindow {
		// Window has expired; reset.
		attempt.count = 0
		attempt.windowStart = now
	}
	attempt.count++
	if attempt.count > loginRateLimit {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}

	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&request); err != nil {
		http.Error(w, "invalid login payload", http.StatusBadRequest)
		return
	}
	if !s.users.Valid(request.Username, request.Password) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.sessions == nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	token, expires, err := s.sessions.Create(s.users.Username())
	if err != nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := secureCookie(r)
	http.SetCookie(w, auth.SessionCookie(token, expires, secure))
	http.SetCookie(w, auth.CSRFCookie(token, expires, secure))
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": s.users.Username()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	secure := secureCookie(r)
	http.SetCookie(w, auth.ClearSessionCookie(secure))
	http.SetCookie(w, auth.ClearCSRFCookie(secure))
	writeJSON(w, map[string]any{"authenticated": false})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.users == nil || !s.users.Enabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}
	username, ok := s.sessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": username})
}

func (s *Server) sessionUser(r *http.Request) (string, bool) {
	if s == nil || s.sessions == nil {
		return "", false
	}
	cookie, err := r.Cookie("bugbarn_session")
	if err != nil {
		return "", false
	}
	return s.sessions.Valid(cookie.Value)
}

// /api/v1/issues/{id} and /api/v1/issues/{id}/events share the same prefix.
func (s *Server) serveIssueRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/resolve") && r.Method == http.MethodPost:
		s.resolveIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/reopen") && r.Method == http.MethodPost:
		s.reopenIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/events"):
		s.listIssueEvents(w, r)
	default:
		s.getIssue(w, r)
	}
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	filter := storage.IssueFilter{
		Sort:   q.Get("sort"),
		Status: q.Get("status"),
		Query:  q.Get("q"),
	}
	// Any query params not used for standard filtering are treated as facet filters.
	knownParams := map[string]bool{"sort": true, "status": true, "q": true}
	for key, vals := range q {
		if knownParams[key] || len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			continue
		}
		if filter.Facets == nil {
			filter.Facets = make(map[string]string)
		}
		filter.Facets[key] = vals[0]
	}
	issues, err := s.service.ListIssuesFiltered(r.Context(), filter)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issues": issues})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimPrefix(r.URL.Path, "/api/v1/issues/")
	issue, err := s.service.GetIssue(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) listIssueEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/events")
	events, err := s.service.ListIssueEvents(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	eventID := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	event, err := s.service.GetEvent(r.Context(), eventID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"event": event})
}

func (s *Server) listRecentEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	since := time.Now().UTC().Add(-15 * time.Minute)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			since = parsed
		}
	}
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			since = time.Now().UTC().Add(-parsed)
		}
	}
	events, err := s.service.ListLiveEvents(r.Context(), limit, since)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

// serveFacetsRoute handles GET /api/v1/facets and GET /api/v1/facets/{key}.
func (s *Server) serveFacetsRoute(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	// Trim the base prefix then check whether a key is present.
	suffix := strings.TrimPrefix(r.URL.Path, "/api/v1/facets")
	suffix = strings.TrimPrefix(suffix, "/")

	if suffix == "" {
		// GET /api/v1/facets — list all facet keys.
		projectID, ok := storage.ProjectIDFromContext(r.Context())
		if !ok {
			projectID = s.store.DefaultProjectID()
		}
		keys, err := s.service.ListFacetKeys(r.Context(), projectID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		if keys == nil {
			keys = []string{}
		}
		writeJSON(w, map[string]any{"keys": keys})
		return
	}

	// GET /api/v1/facets/{key} — list values for a key.
	projectID, ok := storage.ProjectIDFromContext(r.Context())
	if !ok {
		projectID = s.store.DefaultProjectID()
	}
	values, err := s.service.ListFacetValues(r.Context(), projectID, suffix)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if values == nil {
		values = []string{}
	}
	writeJSON(w, map[string]any{"key": suffix, "values": values})
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	// Parse optional ?since= parameter to resume from a specific timestamp.
	since := time.Now().UTC().Add(-15 * time.Minute)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed.UTC()
		} else if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			since = parsed.UTC()
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	pollTicker := time.NewTicker(time.Second)
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()
	defer keepaliveTicker.Stop()

	// Track the latest timestamp seen so we only emit new events.
	cursor := since

	for {
		select {
		case <-ctx.Done():
			return
		case <-keepaliveTicker.C:
			if _, err := fmt.Fprint(w, ":\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-pollTicker.C:
			events, err := s.service.ListLiveEvents(ctx, 100, cursor)
			if err != nil {
				return
			}
			for i := len(events) - 1; i >= 0; i-- {
				ev := events[i]
				ts := ev.ReceivedAt
				if ev.ObservedAt.After(ts) {
					ts = ev.ObservedAt
				}
				if !ts.After(cursor) {
					continue
				}
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
				if ts.After(cursor) {
					cursor = ts
				}
			}
			flusher.Flush()
		}
	}
}

func (s *Server) serveSettingsRoute(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		settings, err := s.service.GetSettings(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"settings": settings})
	case http.MethodPut, http.MethodPost:
		values, err := decodeStringMap(w, r)
		if err != nil {
			http.Error(w, "invalid settings payload", http.StatusBadRequest)
			return
		}
		if err := s.service.UpdateSettings(r.Context(), values); err != nil {
			writeStorageError(w, err)
			return
		}
		settings, err := s.service.GetSettings(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"settings": settings})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveReleasesRoot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		releases, err := s.service.ListReleases(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"releases": releases})
	case http.MethodPost:
		var request struct {
			Name        string `json:"name"`
			Environment string `json:"environment"`
			ObservedAt  string `json:"observedAt"`
			Version     string `json:"version"`
			CommitSHA   string `json:"commitSha"`
			URL         string `json:"url"`
			Notes       string `json:"notes"`
			CreatedBy   string `json:"createdBy"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			http.Error(w, "invalid release payload", http.StatusBadRequest)
			return
		}
		release := storage.Release{
			Name:        request.Name,
			Environment: request.Environment,
			Version:     request.Version,
			CommitSHA:   request.CommitSHA,
			URL:         request.URL,
			Notes:       request.Notes,
			CreatedBy:   request.CreatedBy,
		}
		if parsed, err := time.Parse(time.RFC3339Nano, request.ObservedAt); err == nil {
			release.ObservedAt = parsed
		}
		item, err := s.service.CreateRelease(r.Context(), release)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveReleaseRoute(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	releaseID := strings.TrimPrefix(r.URL.Path, "/api/v1/releases/")
	switch r.Method {
	case http.MethodGet:
		item, err := s.service.GetRelease(r.Context(), releaseID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodPut:
		var request struct {
			Name        string `json:"name"`
			Environment string `json:"environment"`
			ObservedAt  string `json:"observedAt"`
			Version     string `json:"version"`
			CommitSHA   string `json:"commitSha"`
			URL         string `json:"url"`
			Notes       string `json:"notes"`
			CreatedBy   string `json:"createdBy"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			http.Error(w, "invalid release payload", http.StatusBadRequest)
			return
		}
		release := storage.Release{
			Name:        request.Name,
			Environment: request.Environment,
			Version:     request.Version,
			CommitSHA:   request.CommitSHA,
			URL:         request.URL,
			Notes:       request.Notes,
			CreatedBy:   request.CreatedBy,
		}
		if parsed, err := time.Parse(time.RFC3339Nano, request.ObservedAt); err == nil {
			release.ObservedAt = parsed
		}
		item, err := s.service.UpdateRelease(r.Context(), releaseID, release)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"release": item})
	case http.MethodDelete:
		if err := s.service.DeleteRelease(r.Context(), releaseID); err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveAlertsRoot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		alerts, err := s.service.ListAlerts(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"alerts": alerts})
	case http.MethodPost:
		request, err := decodeAnyMap(w, r)
		if err != nil {
			http.Error(w, "invalid alert payload", http.StatusBadRequest)
			return
		}
		item, err := s.service.CreateAlert(r.Context(), alertFromRequest(request))
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"alert": item})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveAlertRoute(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	alertID := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/")
	switch r.Method {
	case http.MethodGet:
		item, err := s.service.GetAlert(r.Context(), alertID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"alert": item})
	case http.MethodPut:
		request, err := decodeAnyMap(w, r)
		if err != nil {
			http.Error(w, "invalid alert payload", http.StatusBadRequest)
			return
		}
		item, err := s.service.UpdateAlert(r.Context(), alertID, alertFromRequest(request))
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"alert": item})
	case http.MethodDelete:
		if err := s.service.DeleteAlert(r.Context(), alertID); err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) resolveIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/resolve")
	item, err := s.service.ResolveIssue(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": item})
}

func (s *Server) reopenIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/reopen")
	item, err := s.service.ReopenIssue(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": item})
}

func (s *Server) listSourceMaps(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := s.service.ListSourceMaps(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if items == nil {
		items = []storage.SourceMapMeta{}
	}
	writeJSON(w, map[string]any{"sourceMaps": items})
}

func (s *Server) uploadSourceMap(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid source map payload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("source_map")
	if err != nil {
		http.Error(w, "source_map is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	blob, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "unable to read source map", http.StatusBadRequest)
		return
	}

	upload := storage.SourceMapUpload{
		Release:     r.FormValue("release"),
		Dist:        r.FormValue("dist"),
		BundleURL:   r.FormValue("bundle_url"),
		Name:        r.FormValue("source_map_name"),
		ContentType: header.Header.Get("Content-Type"),
		Blob:        blob,
	}
	item, err := s.service.UploadSourceMap(r.Context(), upload)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"artifactId": item.ID,
		"release":    item.Release,
		"dist":       item.Dist,
		"bundleUrl":  item.BundleURL,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(dest)
}

func decodeAnyMap(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeStringMap(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	payload, err := decodeAnyMap(w, r)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(payload))
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case float64:
			out[key] = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(typed, 'f', -1, 64), "0"), ".")
		case bool:
			if typed {
				out[key] = "true"
			} else {
				out[key] = "false"
			}
		case nil:
			out[key] = ""
		default:
			raw, _ := json.Marshal(typed)
			out[key] = string(raw)
		}
	}
	return out, nil
}

func alertFromRequest(payload map[string]any) storage.Alert {
	alert := storage.Alert{
		Name:     stringValue(payload["name"]),
		Severity: stringValue(payload["severity"]),
		Enabled:  true,
		Rule:     map[string]any{},
	}
	if enabled, ok := payload["enabled"].(bool); ok {
		alert.Enabled = enabled
	}
	for _, key := range []string{"condition", "query", "target"} {
		if value, ok := payload[key]; ok && value != nil {
			alert.Rule[key] = value
		}
	}
	for key, value := range payload {
		if key == "name" || key == "severity" || key == "enabled" || key == "condition" || key == "query" || key == "target" {
			continue
		}
		alert.Rule[key] = value
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	return alert
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, "storage error", http.StatusInternalServerError)
}

// setCORSHeaders applies CORS policy. We never use * because BugBarn uses
// cookies (credentials), which require an explicit reflected origin.
//
//   - If BUGBARN_ALLOWED_ORIGINS is set (parsed into s.allowedOrigins), any
//     request whose Origin matches the list is reflected with credentials.
//   - Otherwise we only reflect the origin when it is same-origin (Origin
//     host == Host header), so localhost dev still works without configuration.
//   - Vary: Origin is always emitted so CDNs don't cache the wrong value.
func (s *Server) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key, x-bugbarn-csrf, x-bugbarn-project")

	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	if len(s.allowedOrigins) > 0 {
		for _, allowed := range s.allowedOrigins {
			if strings.EqualFold(origin, allowed) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				return
			}
		}
		// Origin not in the explicit list — don't set ACAO.
		return
	}

	// No explicit list: allow same-origin only.
	if sameOrigin(origin, r.Host) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// sameOrigin returns true when the request Origin matches the Host header,
// i.e. the browser and the API server are on the same host+port.
func sameOrigin(origin, host string) bool {
	// origin is a full URL like "http://localhost:8080"; strip the scheme.
	stripped := origin
	if i := strings.Index(stripped, "://"); i >= 0 {
		stripped = stripped[i+3:]
	}
	// Remove any trailing slash.
	stripped = strings.TrimRight(stripped, "/")
	// host may or may not include port; compare case-insensitively.
	return strings.EqualFold(stripped, host)
}

// isCSRFProtected returns true for state-changing requests that are NOT the
// ingest endpoint (which uses API key auth) and NOT login/logout.
func isCSRFProtected(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
		// Ingest and login/logout are excluded.
		switch r.URL.Path {
		case "/api/v1/events", "/api/v1/login", "/api/v1/logout":
			return false
		}
		return true
	}
	return false
}

// validCSRF checks the double-submit cookie pattern: the X-BugBarn-CSRF
// header must match the CSRF token derived from the session cookie.
func (s *Server) validCSRF(r *http.Request) bool {
	sessionCookie, err := r.Cookie("bugbarn_session")
	if err != nil {
		return false
	}
	expected := auth.CSRFToken(sessionCookie.Value)
	provided := r.Header.Get("X-BugBarn-CSRF")
	return provided == expected
}

// clientIP extracts the best-effort client IP from the request.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// Take the first (leftmost) address — the original client.
		if idx := strings.Index(forwarded, ","); idx > 0 {
			return strings.TrimSpace(forwarded[:idx])
		}
		return strings.TrimSpace(forwarded)
	}
	// RemoteAddr is "ip:port".
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) serveProjectsRoot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		projects, err := s.store.ListProjects(r.Context())
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"projects": projects})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			http.Error(w, "invalid project payload", http.StatusBadRequest)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if req.Slug == "" {
			req.Slug = slugify(req.Name)
		}
		p, err := s.store.CreateProject(r.Context(), req.Name, req.Slug)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, map[string]any{"project": p})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	type safeKey struct {
		ID         int64     `json:"id"`
		Name       string    `json:"name"`
		ProjectID  int64     `json:"projectId"`
		CreatedAt  time.Time `json:"createdAt"`
		LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	}
	out := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, safeKey{
			ID:         k.ID,
			Name:       k.Name,
			ProjectID:  k.ProjectID,
			CreatedAt:  k.CreatedAt,
			LastUsedAt: k.LastUsedAt,
		})
	}
	writeJSON(w, map[string]any{"apiKeys": out})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/apikeys/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid api key id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteAPIKey(r.Context(), id); err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var result strings.Builder
	prevDash := true // avoid leading dash
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			result.WriteRune(ch)
			prevDash = false
		} else if !prevDash {
			result.WriteRune('-')
			prevDash = true
		}
	}
	return strings.TrimRight(result.String(), "-")
}
