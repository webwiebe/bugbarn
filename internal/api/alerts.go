package api

import (
	"net/http"
	"strings"
)

func (s *Server) serveAlertsRoot(w http.ResponseWriter, r *http.Request) {
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		alerts, err := s.alerts.List(r.Context())
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
		item, err := s.alerts.Create(r.Context(), alertFromRequest(request))
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
	if s.alerts == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	alertID := strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/")
	switch r.Method {
	case http.MethodGet:
		item, err := s.alerts.Get(r.Context(), alertID)
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
		item, err := s.alerts.Update(r.Context(), alertID, alertFromRequest(request))
		if err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"alert": item})
	case http.MethodDelete:
		if err := s.alerts.Delete(r.Context(), alertID); err != nil {
			writeStorageError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
