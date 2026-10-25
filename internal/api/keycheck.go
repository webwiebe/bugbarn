package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
)

// requireIngestKey checks the request's API key for the ingest-style routes
// and writes the refusal itself. A key that was looked up and not found is a
// permanent 401; a lookup the store failed is a retryable 503, so a client
// does not drop its data or retire a good key over a blip. A nil ingest
// handler means no key store is configured and every key passes.
func (s *Server) requireIngestKey(w http.ResponseWriter, r *http.Request) bool {
	if s.ingestHandler == nil {
		return true
	}
	_, _, ok, err := s.ingestHandler.CheckAPIKey(r)
	if err != nil {
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return false
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}
