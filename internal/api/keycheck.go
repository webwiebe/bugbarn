package api

import (
	"errors"
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/ingestresp"
)

// requireIngestKey checks the request's API key for the ingest-style routes
// and writes the refusal itself. A key that was looked up and not found is a
// permanent 401; a lookup the store failed is a retryable 503, so a client
// does not drop its data or retire a good key over a blip. A nil ingest
// handler means no key store is configured and every key passes. A bound key
// that names another project is refused by requireOwnProject.
func (s *Server) requireIngestKey(w http.ResponseWriter, r *http.Request) bool {
	if s.ingestHandler == nil {
		return true
	}
	projectID, scope, ok, err := s.ingestHandler.CheckAPIKey(r)
	if err != nil {
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
		return false
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return s.requireOwnProject(w, r, projectID, scope)
}

// requireOwnProject confines an ingest write by a bound, non-full-scope key to
// the key's own project, the way the authenticated routes are (keybind.go): a
// request that names another project in X-BugBarn-Project is a permanent 403.
// A slug no project has counts as another project, because a bound key never
// creates one. A failed project lookup is the retryable 503. Full-scope keys,
// unbound keys and requests without the header pass.
func (s *Server) requireOwnProject(w http.ResponseWriter, r *http.Request, keyProjectID int64, scope string) bool {
	bound, confined := confinedProject(keyProjectID, scope)
	slug := r.Header.Get("X-BugBarn-Project")
	if !confined || slug == "" || s.projects == nil {
		return true
	}
	_, err := s.projectForSlug(r.WithContext(withBoundProject(r.Context(), bound)), slug)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errForeignProject):
		s.logger.Warn("ingest: bound key named another project", "method", r.Method, "path", r.URL.Path)
		ingestresp.WriteDropped(w, ingestresp.DropForbidden)
	default:
		ingestresp.WriteDropped(w, ingestresp.DropUnavailable)
	}
	return false
}
