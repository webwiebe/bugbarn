package api

import (
	"net/http"

	"github.com/wiebe-xyz/bugbarn/internal/keytouch"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
)

// registerInternalRoutes registers the writer-internal endpoints. They answer
// only to HMAC-signed reader requests, bypass the cookie and API key pipeline
// and are left out of api/openapi.yaml on purpose. Each is 404 until enabled,
// then 405 to any method but POST.
func (s *Server) registerInternalRoutes(rt *router) {
	g := rt.group()

	// CQRS session endpoints: create / get-or-refresh / delete / delete-by-sid /
	// delete-by-sub. The {op...} wildcard keeps an empty or unknown op on the
	// handler's own 404, after the signature check.
	sessions := sessionstore.InternalPathPrefix + "{op...}"
	g.handle("POST "+sessions, s.serveInternalSessions)
	g.fallback(sessions, s.internalMethodFallback(func() bool {
		return len(s.internalSessionSecret) > 0 && s.sessionStore != nil
	}))

	// The reader-to-writer API key last-use batches use the same scheme.
	g.handle("POST "+keytouch.Path, s.serveInternalKeyTouch)
	g.fallback(keytouch.Path, s.internalMethodFallback(func() bool {
		return len(s.internalSessionSecret) > 0 && s.keyToucher != nil
	}))
}

// internalMethodFallback answers a non-POST request to an internal endpoint:
// 404 while the endpoint is disabled, 405 once it is enabled.
func (s *Server) internalMethodFallback(enabled func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enabled() {
			http.NotFound(w, r)
			return
		}
		methodNotAllowed("")(w, r)
	}
}
