package api

import "net/http"

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	alerts, err := s.alerts.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"alerts": alerts})
}

func (s *Server) createAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	request, err := decodeAnyMap(w, r)
	if err != nil {
		http.Error(w, "invalid alert payload", http.StatusBadRequest)
		return
	}
	item, err := s.alerts.Create(r.Context(), alertFromRequest(request))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"alert": item})
}

func (s *Server) getAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	item, err := s.alerts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"alert": item})
}

func (s *Server) updateAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	request, err := decodeAnyMap(w, r)
	if err != nil {
		http.Error(w, "invalid alert payload", http.StatusBadRequest)
		return
	}
	item, err := s.alerts.Update(r.Context(), r.PathValue("id"), alertFromRequest(request))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"alert": item})
}

func (s *Server) deleteAlert(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.alerts.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}
