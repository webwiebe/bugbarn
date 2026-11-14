package api

import "net/http"

// registerPublicRoutes registers the endpoints that need no authentication.
func (s *Server) registerPublicRoutes(rt *router) {
	// No CORS headers: the setup page and the manifests are fetched by agents and
	// by IAMBarn, never by a credentialed browser.
	bare := rt.group()
	bare.handle("GET /.well-known/iambarn-theme.json", s.serveThemeManifest)
	bare.handle("GET /analytics.js", s.serveAnalyticsSnippet)
	s.registerSetupRoutes(bare)

	// The general CORS policy applies to the rest.
	pub := rt.group(s.generalCORS)
	pub.handle("GET /api/v1/openapi.yaml", s.serveOpenAPISpec)
	pub.handle("GET /api/docs", s.serveAPIDocs)
	pub.handle("GET /api/v1/health", s.serveHealth)
	pub.handle("GET /api/v1/version", s.serveVersion)
	pub.handle("GET /api/v1/runtime-config", s.serveRuntimeConfig)
	pub.handle("GET /api/v1/me", s.me)
	pub.handle("GET /api/v1/oidc/login", s.oidcLogin)
	pub.handle("GET /api/v1/oidc/callback", s.oidcCallback)
	pub.handle("GET /api/v1/oidc/logged-out", s.oidcLoggedOut)
	pub.handle("POST /api/v1/login", s.forwarded(s.login))
	pub.handle("POST /api/v1/logout", s.forwarded(s.logout))
	// Authenticated by the logout token's signature, no CSRF. Session rows live
	// behind the writer, so readers forward the POST.
	pub.handle("POST /api/v1/oidc/backchannel-logout", s.forwarded(s.oidcBackchannelLogout))
}

// forwarded runs a session-writing handler on the writer: a reader forwards the
// request there.
func (s *Server) forwarded(local http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { s.forwardWrite(w, r, local) }
}

// registerSetupRoutes registers the setup page and the agent key requests. Both
// are public and rate-limited per IP.
func (s *Server) registerSetupRoutes(g group) {
	// GET /api/v1/setup/{slug...} is the setup page. Anything under the prefix
	// reaches it, and a path that is not a single segment is an invalid slug
	// (400), as it always was.
	g.handle("GET /api/v1/setup/{slug...}", s.serveSetup)

	// Agent key requests:
	//
	//	POST /api/v1/setup/{slug}/key-requests        open a request
	//	GET  /api/v1/setup/{slug}/key-requests/{id}   poll it
	//
	// The trailing-slash forms are served too. A wrong method answers 405 with
	// Allow ahead of authentication. The setup wildcard above overlaps these
	// paths, so each method is listed rather than matched by a method-less
	// pattern.
	collection := "/api/v1/setup/{slug}/key-requests"
	item := collection + "/{id}"
	g.handle("POST "+collection, s.createKeyRequest)
	g.fallback("POST "+collection+"/{$}", s.createKeyRequest)
	g.handle("GET "+item, s.pollKeyRequest)
	g.fallback("GET "+item+"/{$}", s.pollKeyRequest)
	for _, path := range []string{collection, collection + "/{$}"} {
		rejectMethods(g, path, "GET, POST", "GET", "HEAD", "PUT", "PATCH", "DELETE", "OPTIONS")
	}
	for _, path := range []string{item, item + "/{$}"} {
		rejectMethods(g, path, "GET, POST", "POST", "PUT", "PATCH", "DELETE", "OPTIONS")
	}
}
