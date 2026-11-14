package storage

import (
	"encoding/json"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func marshalEvent(evt event.Event) ([]byte, error) {
	return json.Marshal(evt)
}

func fingerprintFromEvent(evt event.Event) string {
	exceptionType := strings.TrimSpace(evt.Exception.Type)
	message := strings.TrimSpace(evt.Exception.Message)
	if message == "" {
		message = strings.TrimSpace(evt.Message)
	}

	var title string
	switch {
	case exceptionType != "" && message != "":
		title = exceptionType + ": " + message
	case exceptionType != "":
		title = exceptionType
	default:
		title = message
	}

	return normalizeTitle(title)
}

func mustMarshalStrings(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func unmarshalStringSlice(raw []byte, dest *[]string) error {
	if len(raw) == 0 {
		*dest = nil
		return nil
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		*dest = nil
		return err
	}
	return nil
}
