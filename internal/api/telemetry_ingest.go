package api

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
)

// TelemetryIngester stores Vector telemetry batches on the writer. Satisfied
// by *telemetry.Ingester.
type TelemetryIngester interface {
	Ingest(ctx context.Context, kind string, body []byte) error
}

const (
	telemetrySecurityPath = "/api/v1/telemetry/security"
	telemetryMetricsPath  = "/api/v1/telemetry/metrics"
)

// SetTelemetry wires telemetry ingest. ingester is nil on readers, which
// forward. project is the slug whose ingest-scoped keys may post telemetry.
func (s *Server) SetTelemetry(ingester TelemetryIngester, project string) {
	s.telemetryIngester = ingester
	s.telemetryProject = project
}

func telemetryKind(path string) string {
	switch path {
	case telemetrySecurityPath:
		return queue.KindSecurity
	case telemetryMetricsPath:
		return queue.KindMetrics
	}
	return ""
}

// serveTelemetryIngestEndpoint handles POST /api/v1/telemetry/{security,metrics}
// and answers 405 to methods other than GET and POST.
// No CORS: the senders are Vector agents, never browsers.
func (s *Server) serveTelemetryIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	kind := telemetryKind(r.URL.Path)
	// GET on the same path is the authenticated read API (telemetry_read.go).
	if kind == "" || r.Method == http.MethodGet {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if deny := s.telemetryAuthorized(r); deny != nil {
		ingestresp.WriteDropped(w, *deny)
		return true
	}
	switch {
	case s.ingestSpool != nil:
		s.ingestSpool.Forward(w, r)
	case s.writeForwarder != nil:
		s.writeForwarder.Forward(w, r)
	default:
		s.ingestTelemetryLocal(w, r, kind)
	}
	return true
}

// telemetryAuthorized accepts a full-scope key, or an ingest-scoped key bound
// to the telemetry project. Any other ingest key is refused: browser SDK keys
// are public, and telemetry feeds detections that page someone, so a page
// that ships a key must not be able to forge security events.
//
// It returns nil when the request may proceed, DropUnavailable (retryable)
// when the key store failed, and DropUnauthorized otherwise.
func (s *Server) telemetryAuthorized(r *http.Request) *ingestresp.Drop {
	deny := ingestresp.DropUnauthorized
	if s.ingestHandler == nil {
		return &deny
	}
	pid, scope, ok, keyErr := s.ingestHandler.CheckAPIKey(r)
	if keyErr != nil {
		unavailable := ingestresp.DropUnavailable
		return &unavailable
	}
	if !ok {
		return &deny
	}
	if scope == "full" {
		return nil
	}
	if s.projects == nil || s.telemetryProject == "" || pid == 0 {
		return &deny
	}
	proj, err := s.projects.BySlug(r.Context(), s.telemetryProject)
	if err != nil || proj.ID != pid {
		return &deny
	}
	return nil
}

func (s *Server) ingestTelemetryLocal(w http.ResponseWriter, r *http.Request, kind string) {
	if s.telemetryIngester == nil {
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return
	}
	limit := int64(1 << 20)
	if s.ingestHandler != nil && s.ingestHandler.MaxBodyBytes() > 0 {
		limit = s.ingestHandler.MaxBodyBytes()
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		ingestresp.WriteDropped(w, ingestresp.DropMalformed)
		return
	}
	if int64(len(body)) > limit {
		ingestresp.WriteDropped(w, ingestresp.DropTooLarge)
		return
	}
	// Detections already saw the batch once it parsed, so a failed insert is
	// still a 202: a retry would double-count. Only a file that never opened
	// asks the sender to come back later.
	if err := s.telemetryIngester.Ingest(r.Context(), kind, body); errors.Is(err, telemetry.ErrUnavailable) {
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return
	}
	ingestresp.WriteAccepted(w, "")
}
