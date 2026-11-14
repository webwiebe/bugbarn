package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/detect"
	"github.com/wiebe-xyz/bugbarn/internal/service/detectionrules"
)

// DetectionRules returns the rules service, so the writer can hook the
// running detection engine to it.
func (s *Server) DetectionRules() *detectionrules.Service { return s.detectionRules }

func (s *Server) listDetectionRules(w http.ResponseWriter, r *http.Request) {
	if s.detectionRules == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	rules, err := s.detectionRules.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"rules": rules})
}

func (s *Server) putDetectionRule(w http.ResponseWriter, r *http.Request) {
	if s.detectionRules == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	var rule detect.Rule
	if err := decodeJSON(w, r, &rule); err != nil {
		http.Error(w, "invalid detection rule payload", http.StatusBadRequest)
		return
	}
	view, err := s.detectionRules.Put(r.Context(), r.PathValue("id"), rule)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"rule": view})
}

func (s *Server) deleteDetectionRule(w http.ResponseWriter, r *http.Request) {
	if s.detectionRules == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.detectionRules.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": true})
}
