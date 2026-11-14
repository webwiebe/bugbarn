package api

import "net/http"

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.projects.GetSettings(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"settings": settings})
}

// updateSettings serves both PUT and POST /api/v1/settings.
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	values, err := decodeStringMap(w, r)
	if err != nil {
		http.Error(w, "invalid settings payload", http.StatusBadRequest)
		return
	}
	if err := s.projects.UpdateSettings(r.Context(), values); err != nil {
		writeServiceError(w, err)
		return
	}
	s.getSettings(w, r)
}
