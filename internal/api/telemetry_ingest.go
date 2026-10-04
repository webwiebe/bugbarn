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

// serveTelemetryIngestEndpoint handles POST /api/v1/telemetry/{security,metrics}.
// No CORS: the senders are Vector agents, never browsers.
func (s *Server) serveTelemetryIngestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	kind := telemetryKind(r.URL.Path)
	if kind == "" {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if !s.telemetryAuthorized(r) {
		ingestresp.WriteDropped(w, ingestresp.DropUnauthorized)
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
func (s *Server) telemetryAuthorized(r *http.Request) bool {
	if s.ingestHandler == nil {
		return false
	}
	pid, scope, ok := s.ingestHandler.APIKeyProjectScope(r)
	if !ok {
		return false
	}
	if scope == "full" {
		return true
	}
	if s.projects == nil || s.telemetryProject == "" || pid == 0 {
		return false
	}
	proj, err := s.projects.BySlug(r.Context(), s.telemetryProject)
	return err == nil && proj.ID == pid
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
