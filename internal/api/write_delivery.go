package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// deliverWrite sends an ingest write where this process keeps its writes. A
// reader with a spool forwarder appends to the spool, a reader with a writer
// forwarder relays it, and the writer or the monolith runs local.
func (s *Server) deliverWrite(w http.ResponseWriter, r *http.Request, local http.HandlerFunc) {
	if s.ingestSpool != nil {
		s.ingestSpool.Forward(w, r)
		return
	}
	s.forwardWrite(w, r, local)
}

// forwardWrite relays a write to the writer when this process is a reader, and
// runs local otherwise. Session writes use it directly because they have no spool.
func (s *Server) forwardWrite(w http.ResponseWriter, r *http.Request, local http.HandlerFunc) {
	if s.writeForwarder != nil {
		s.writeForwarder.Forward(w, r)
		return
	}
	local(w, r)
}

// handleLogsIngestPost serves a POST to the log-ingest endpoint: it validates
// the API key (the route group set the CORS headers), forwards to the spool/writer when configured, and
// otherwise resolves the project and persists the batch locally.
func (s *Server) handleLogsIngestPost(w http.ResponseWriter, r *http.Request) {
	if !s.requireIngestKey(w, r) {
		return
	}
	s.deliverWrite(w, r, s.ingestLogsLocally)
}

// ingestLogsLocally persists a log batch in this process: the writer, or the
// monolith. Readers never get here, deliverWrite sends their batches on.
func (s *Server) ingestLogsLocally(w http.ResponseWriter, r *http.Request) {
	// Resolve project from API key / header.
	var logProjectID int64
	if s.ingestHandler != nil {
		pid, _, ok, err := s.ingestHandler.CheckAPIKey(r)
		if err != nil {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "api key lookup failed, retry", http.StatusServiceUnavailable)
			return
		}
		if ok && pid > 0 {
			logProjectID = pid
		}
	}
	if slug := r.Header.Get("X-BugBarn-Project"); slug != "" && s.projects != nil {
		if proj, err := s.projects.Ensure(r.Context(), slug); err == nil {
			logProjectID = proj.ID
		}
	}
	if logProjectID > 0 {
		r = r.WithContext(domain.WithProjectID(r.Context(), logProjectID))
	}
	s.serveLogsIngest(w, r)
}
