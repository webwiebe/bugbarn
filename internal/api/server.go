package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Server struct {
	ingestHandler *ingest.Handler
	store         *storage.Store
	users         *auth.UserAuthenticator
	sessions      *auth.SessionManager
}

func NewServer(ingestHandler *ingest.Handler, store *storage.Store) *Server {
	return &Server{ingestHandler: ingestHandler, store: store}
}

func NewServerWithAuth(ingestHandler *ingest.Handler, store *storage.Store, users *auth.UserAuthenticator, sessions *auth.SessionManager) *Server {
	return &Server{ingestHandler: ingestHandler, store: store, users: users, sessions: sessions}
}

type route struct {
	method  string
	path    string
	prefix  bool
	handler http.HandlerFunc
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch {
	case r.URL.Path == "/api/v1/events" && r.Method == http.MethodPost:
		s.ingestHandler.ServeHTTP(w, r)
	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case r.URL.Path == "/api/v1/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		s.me(w, r)
	default:
		for _, route := range s.queryRoutes() {
			if route.matches(r.Method, r.URL.Path) {
				if !s.authorized(r) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				route.handler(w, r)
				return
			}
		}

		http.NotFound(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.users == nil || !s.users.Enabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
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
	http.SetCookie(w, auth.SessionCookie(token, expires, secureCookie(r)))
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": s.users.Username()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, auth.ClearSessionCookie(secureCookie(r)))
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

func (s *Server) authorized(r *http.Request) bool {
	if s == nil || s.users == nil || !s.users.Enabled() {
		return true
	}
	_, ok := s.sessionUser(r)
	return ok
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

func (s *Server) queryRoutes() []route {
	return []route{
		{method: http.MethodGet, path: "/api/v1/issues", handler: s.listIssues},
		{method: http.MethodGet, path: "/api/v1/issues/", prefix: true, handler: s.serveIssueRoute},
		{method: http.MethodGet, path: "/api/v1/events/", prefix: true, handler: s.getEvent},
		{method: http.MethodGet, path: "/api/v1/live/events", handler: s.listRecentEvents},
	}
}

func (r route) matches(method, path string) bool {
	if r.method != method {
		return false
	}
	if r.prefix {
		return strings.HasPrefix(path, r.path)
	}
	return path == r.path
}

// /api/v1/issues/{id} and /api/v1/issues/{id}/events share the same prefix.
func (s *Server) serveIssueRoute(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/events") {
		s.listIssueEvents(w, r)
		return
	}
	s.getIssue(w, r)
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issues, err := s.store.ListIssues(r.Context())
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issues": issues})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimPrefix(r.URL.Path, "/api/v1/issues/")
	issue, err := s.store.GetIssue(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": issue})
}

func (s *Server) listIssueEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/events")
	events, err := s.store.ListIssueEvents(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	eventID := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	event, err := s.store.GetEvent(r.Context(), eventID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"event": event})
}

func (s *Server) listRecentEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	events, err := s.store.ListRecentEvents(r.Context(), 50)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key")
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
