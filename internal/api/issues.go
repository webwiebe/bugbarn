package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// serveIssueRoute handles /api/v1/issues/{id} and /api/v1/issues/{id}/events.
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
