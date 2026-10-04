package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/detect"
	"github.com/wiebe-xyz/bugbarn/internal/service/detectionrules"
)

const detectionRulesPath = "/api/v1/detection/rules"

// DetectionRules returns the rules service, so the writer can hook the
// running detection engine to it.
func (s *Server) DetectionRules() *detectionrules.Service { return s.detectionRules }

func (s *Server) dispatchDetectionRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == detectionRulesPath && r.Method == http.MethodGet:
		s.listDetectionRules(w, r)
	case strings.HasPrefix(r.URL.Path, detectionRulesPath+"/"):
		s.serveDetectionRule(w, r)
	default:
		return false
	}
	return true
}

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

func (s *Server) serveDetectionRule(w http.ResponseWriter, r *http.Request) {
	if s.detectionRules == nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, detectionRulesPath+"/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var rule detect.Rule
		if err := decodeJSON(w, r, &rule); err != nil {
			http.Error(w, "invalid detection rule payload", http.StatusBadRequest)
			return
		}
		view, err := s.detectionRules.Put(r.Context(), id, rule)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"rule": view})
	case http.MethodDelete:
		if err := s.detectionRules.Delete(r.Context(), id); err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
