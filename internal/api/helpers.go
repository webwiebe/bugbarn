package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, "storage error", http.StatusInternalServerError)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(dest)
}

func decodeAnyMap(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeStringMap(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	payload, err := decodeAnyMap(w, r)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(payload))
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case float64:
			out[key] = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(typed, 'f', -1, 64), "0"), ".")
		case bool:
			if typed {
				out[key] = "true"
			} else {
				out[key] = "false"
			}
		case nil:
			out[key] = ""
		default:
			raw, _ := json.Marshal(typed)
			out[key] = string(raw)
		}
	}
	return out, nil
}

func alertFromRequest(payload map[string]any) storage.Alert {
	alert := storage.Alert{
		Name:       stringValue(payload["name"]),
		Severity:   stringValue(payload["severity"]),
		WebhookURL: stringValue(payload["webhook_url"]),
		Condition:  stringValue(payload["condition"]),
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
		"webhook_url": true, "condition": true, "threshold": true, "cooldown_minutes": true,
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

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var result strings.Builder
	prevDash := true // avoid leading dash
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			result.WriteRune(ch)
			prevDash = false
		} else if !prevDash {
			result.WriteRune('-')
			prevDash = true
		}
	}
	return strings.TrimRight(result.String(), "-")
}
