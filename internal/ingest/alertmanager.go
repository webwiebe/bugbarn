package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
)

type alertmanagerEnvelope struct {
	Status string              `json:"status"`
	Alerts []alertmanagerAlert `json:"alerts"`
}

type alertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// AlertmanagerEvents turns an Alertmanager webhook envelope into native
// BugBarn event payloads. Keeping this transformation at the boundary means
// the normal privacy, durable-spool, and persistence paths remain shared with
// every other event source.
func AlertmanagerEvents(raw []byte) ([][]byte, error) {
	var envelope alertmanagerEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Alerts == nil {
		return nil, fmt.Errorf("alertmanager payload has no alerts")
	}

	events := make([][]byte, 0, len(envelope.Alerts))
	for _, alert := range envelope.Alerts {
		encoded, err := alertmanagerPayload(alert, envelope.Status)
		if err != nil {
			return nil, err
		}
		events = append(events, encoded)
	}
	return events, nil
}

func alertmanagerPayload(alert alertmanagerAlert, envelopeStatus string) ([]byte, error) {
	alertName := strings.TrimSpace(alert.Labels["alertname"])
	summary := strings.TrimSpace(alert.Annotations["summary"])
	message := summary
	if message == "" {
		message = alertName
	}
	if alertName != "" && summary != "" {
		// Include the rule name because BugBarn fingerprints messages but does
		// not treat alertname as stable context.
		message = alertName + ": " + summary
	}
	status := alert.Status
	if status == "" {
		status = envelopeStatus
	}
	severity := alert.Labels["severity"]
	if strings.EqualFold(status, "resolved") {
		severity = "info"
	}
	payload := map[string]any{
		"body":              message,
		"severityText":      severity,
		"observedTimestamp": alert.StartsAt,
		"attributes": map[string]any{
			"labels":      alert.Labels,
			"description": alert.Annotations["description"],
			"alertmanager": map[string]any{
				"fingerprint":  alert.Fingerprint,
				"generatorURL": alert.GeneratorURL,
				"status":       status,
			},
		},
	}
	if alert.Fingerprint != "" {
		payload["fingerprint"] = "alertmanager:" + alert.Fingerprint
	}
	return json.Marshal(payload)
}
