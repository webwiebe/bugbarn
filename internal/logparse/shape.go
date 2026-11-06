package logparse

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// ErrNoLogsArray reports a JSON log body whose "logs" key is missing, null or
// not an array. Such a body used to answer 204 (direct) or 202 (reader) and
// store nothing, so a sender with the wrong shape never heard about it (#263).
var ErrNoLogsArray = errors.New(`log body needs a "logs" array`)

// IsNDJSON reports whether a Content-Type header names the NDJSON log format.
// Every other content type is read as a JSON {"logs": [...]} body.
func IsNDJSON(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(ct) == "application/x-ndjson"
}

// LogsArray decodes the raw value of a JSON log body's "logs" key into entries.
// A missing key, null or any non-array value is ErrNoLogsArray. An empty array
// is valid and yields no entries.
func LogsArray(raw json.RawMessage) ([]map[string]any, error) {
	v := bytes.TrimSpace(raw)
	if len(v) == 0 || v[0] != '[' {
		return nil, ErrNoLogsArray
	}
	var entries []map[string]any
	if err := json.Unmarshal(v, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// jsonBody is the JSON log body shape, with "logs" kept raw for LogsArray.
type jsonBody struct {
	Logs json.RawMessage `json:"logs"`
}

// DecodeJSONBody decodes a JSON log body of the form {"logs": [...]}. It fails
// on invalid JSON and with ErrNoLogsArray on a body without a "logs" array.
func DecodeJSONBody(body []byte) ([]map[string]any, error) {
	var payload jsonBody
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return LogsArray(payload.Logs)
}
