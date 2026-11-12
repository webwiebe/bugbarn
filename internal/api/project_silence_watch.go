package api

import (
	"net/http"
	"strings"
)

// isSilenceWatchRoute reports whether the request is GET or PUT
// /api/v1/projects/:slug/silence-watch.
func isSilenceWatchRoute(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodPut) &&
		strings.HasSuffix(r.URL.Path, "/silence-watch")
}

// projectSilenceWatch handles GET and PUT /api/v1/projects/:slug/silence-watch:
// whether the silence rules judge the project. An empty silence_watch means
// watched, "off" opts the project out.
func (s *Server) projectSilenceWatch(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/silence-watch")
	if slug == "" {
		http.Error(w, "project slug is required", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		value, err := s.projects.SilenceWatch(r.Context(), slug)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"silence_watch": value})
		return
	}
	var req struct {
		SilenceWatch string `json:"silence_watch"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	value, err := s.projects.UpdateSilenceWatch(r.Context(), slug, req.SilenceWatch)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "silence_watch": value})
}
