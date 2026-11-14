package api

import "net/http"

// getSilenceWatch handles GET /api/v1/projects/{slug}/silence-watch: whether the
// silence rules judge the project. An empty silence_watch means watched, "off"
// opts the project out.
func (s *Server) getSilenceWatch(w http.ResponseWriter, r *http.Request) {
	value, err := s.projects.SilenceWatch(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"silence_watch": value})
}

// putSilenceWatch handles PUT /api/v1/projects/{slug}/silence-watch.
func (s *Server) putSilenceWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SilenceWatch string `json:"silence_watch"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	value, err := s.projects.UpdateSilenceWatch(r.Context(), r.PathValue("slug"), req.SilenceWatch)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "silence_watch": value})
}
