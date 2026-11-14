package api

import (
	"fmt"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

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

func alertFromRequest(payload map[string]any) domain.Alert {
	alert := domain.Alert{
		Name:       stringValue(payload["name"]),
		Severity:   stringValue(payload["severity"]),
		WebhookURL: stringValue(payload["webhook_url"]),
		EmailTo:    stringValue(payload["email_to"]),
		Condition:  stringValue(payload["condition"]),
		Param:      stringValue(payload["param"]),
		Enabled:    true,
		Rule:       map[string]any{},
	}
	if enabled, ok := payload["enabled"].(bool); ok {
		alert.Enabled = enabled
	}
	if threshold, ok := payload["threshold"].(float64); ok {
		alert.Threshold = int(threshold)
	}
	if cooldown, ok := payload["cooldown_minutes"].(float64); ok {
		alert.CooldownMinutes = int(cooldown)
	}
	// Top-level known keys — do not copy into Rule.
	topLevel := map[string]bool{
		"name": true, "severity": true, "enabled": true,
		"webhook_url": true, "email_to": true, "condition": true, "param": true, "threshold": true, "cooldown_minutes": true,
	}
	for _, key := range []string{"query", "target"} {
		if value, ok := payload[key]; ok && value != nil {
			alert.Rule[key] = value
		}
	}
	for key, value := range payload {
		if topLevel[key] || key == "query" || key == "target" {
			continue
		}
		alert.Rule[key] = value
	}
	if alert.Rule == nil {
		alert.Rule = map[string]any{}
	}
	return alert
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}
