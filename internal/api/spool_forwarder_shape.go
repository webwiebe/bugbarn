package api

import (
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	"github.com/wiebe-xyz/bugbarn/internal/normalize"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// bodyHasIngestShape reports whether a body the reader is about to spool has the
// shape the writer will parse. A body that fails here would answer 202 and then
// be dropped by the writer, so the reader answers 400 instead.
func bodyHasIngestShape(path, contentType string, body []byte) bool {
	switch {
	case kindForPath(path) == queue.KindEvent:
		return normalize.Validate(body) == nil
	case kindForPath(path) == queue.KindLog:
		// A JSON log body needs a "logs" array (#263). NDJSON lines are parsed
		// by the writer, which skips a line that does not parse.
		if logparse.IsNDJSON(contentType) {
			return true
		}
		_, err := logparse.DecodeJSONBody(body)
		return err == nil
	case strings.HasPrefix(path, "/api/v1/alertmanager"):
		_, err := ingest.AlertmanagerEvents(body)
		return err == nil
	}
	return true
}

// kindForPath maps an ingest request path to its queue Item kind.
func kindForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/events"):
		return queue.KindEvent
	case strings.HasPrefix(path, "/api/v1/logs"):
		return queue.KindLog
	default:
		// Telemetry paths match exactly; RequestURI may carry a query string.
		p, _, _ := strings.Cut(path, "?")
		return telemetryKind(p)
	}
}
