package api

import "net/http"

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
