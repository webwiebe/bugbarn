package api

import (
	"net/http"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/keytouch"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

// The routes below answer only to HMAC-signed reader requests. They bypass the
// cookie and API key pipeline and are left out of api/openapi.yaml on purpose.

// serveInternalEndpoint routes the writer-internal endpoints that answer only to
// HMAC-signed reader requests. It reports whether the path was one of them.
func (s *Server) serveInternalEndpoint(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case strings.HasPrefix(r.URL.Path, sessionstore.InternalPathPrefix):
		s.serveInternalSessions(w, r)
	case r.URL.Path == keytouch.Path:
		s.serveInternalKeyTouch(w, r)
	default:
		return false
	}
	return true
}
