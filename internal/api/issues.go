package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// serveIssueRoute handles /api/v1/issues/{id} and sub-paths.
func (s *Server) serveIssueRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/resolve") && r.Method == http.MethodPost:
		s.resolveIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/reopen") && r.Method == http.MethodPost:
		s.reopenIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/mute") && r.Method == http.MethodPatch:
		s.muteIssue(w, r)
	case strings.HasSuffix(r.URL.Path, "/unmute") && r.Method == http.MethodPatch:
		s.unmuteIssue(w, r)
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
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			filter.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	requestedLimit := filter.Limit
	filter.Limit = requestedLimit + 1
	// Any query params not used for standard filtering are treated as facet filters.
	knownParams := map[string]bool{"sort": true, "status": true, "q": true, "limit": true, "offset": true}
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

	hasMore := len(issues) > requestedLimit
	if hasMore {
		issues = issues[:requestedLimit]
	}
	writeJSON(w, map[string]any{"issues": issues, "hasMore": hasMore})
}

func (s *Server) issueSparklines(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	ids := r.URL.Query().Get("ids")
	if ids == "" {
		writeJSON(w, map[string]any{"sparklines": map[string][24]int{}})
		return
	}

	parts := strings.Split(ids, ",")
	issueIDs := make([]int64, 0, len(parts))
	displayByRowID := make(map[int64]string, len(parts))
	for _, p := range parts {
		displayID := strings.TrimSpace(p)
		rowID, err := s.store.IssueRowIDByDisplayID(r.Context(), displayID)
		if err != nil {
			continue
		}
		issueIDs = append(issueIDs, rowID)
		displayByRowID[rowID] = displayID
	}

	hourlyCounts, err := s.service.HourlyEventCounts(r.Context(), issueIDs)
	if err != nil {
		hourlyCounts = map[int64][24]int{}
	}

	result := make(map[string][24]int, len(hourlyCounts))
	for id, counts := range hourlyCounts {
		if displayID, ok := displayByRowID[id]; ok {
			result[displayID] = counts
		}
	}
	writeJSON(w, map[string]any{"sparklines": result})
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

// muteIssue handles PATCH /api/v1/issues/:id/mute
// Body: {"mute_mode":"until_regression"|"forever"}
func (s *Server) muteIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/mute")
	var body struct {
		MuteMode string `json:"mute_mode"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		http.Error(w, "invalid mute payload", http.StatusBadRequest)
		return
	}
	item, err := s.service.MuteIssue(r.Context(), issueID, body.MuteMode)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": item})
}

// unmuteIssue handles PATCH /api/v1/issues/:id/unmute
func (s *Server) unmuteIssue(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.service == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	issueID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/issues/"), "/unmute")
	item, err := s.service.UnmuteIssue(r.Context(), issueID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"issue": item})
}
