package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Server struct {
	ingestHandler *ingest.Handler
	store         *storage.Store
}

func NewServer(ingestHandler *ingest.Handler, store *storage.Store) *Server {
	return &Server{ingestHandler: ingestHandler, store: store}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch {
	case r.URL.Path == "/api/v1/events" && r.Method == http.MethodPost:
		s.ingestHandler.ServeHTTP(w, r)
	case r.URL.Path == "/api/v1/issues" && r.Method == http.MethodGet:
		s.listIssues(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/issues/") && strings.HasSuffix(r.URL.Path, "/events") && r.Method == http.MethodGet:
		s.listIssueEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/issues/") && r.Method == http.MethodGet:
		s.getIssue(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/events/") && r.Method == http.MethodGet:
		s.getEvent(w, r)
	case r.URL.Path == "/api/v1/live/events" && r.Method == http.MethodGet:
		s.listRecentEvents(w, r)
	default:
		http.NotFound(w, r)
	}
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
