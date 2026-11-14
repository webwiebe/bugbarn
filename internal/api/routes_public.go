package api

import (
	"net/http"
	"strings"
)

// servePublicEndpoint handles the endpoints that require no authentication but
// do use the general CORS headers (already set by the caller). It returns true
// when it has written a response and the caller should stop.
//
//nolint:gocyclo // flat public-route table mapping path+method to a handler.
func (s *Server) servePublicEndpoint(w http.ResponseWriter, r *http.Request) bool {
	// OpenAPI spec and docs — public, no auth required.
	if r.URL.Path == "/api/v1/openapi.yaml" && r.Method == http.MethodGet {
		s.serveOpenAPISpec(w, r)
		return true
	}
	if r.URL.Path == "/api/docs" && r.Method == http.MethodGet {
		s.serveAPIDocs(w, r)
		return true
	}

	// Public endpoints — no authentication required.
	switch {
	case r.URL.Path == "/api/v1/health" && r.Method == http.MethodGet:
		s.serveHealth(w, r)
		return true
	case r.URL.Path == "/api/v1/version" && r.Method == http.MethodGet:
		s.serveVersion(w, r)
		return true
	case r.URL.Path == "/api/v1/runtime-config" && r.Method == http.MethodGet:
		s.serveRuntimeConfig(w, r)
		return true
	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		s.forwardWrite(w, r, s.login)
		return true
	case r.URL.Path == "/api/v1/logout" && r.Method == http.MethodPost:
		s.forwardWrite(w, r, s.logout)
		return true
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		s.me(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/login" && r.Method == http.MethodGet:
		s.oidcLogin(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/callback" && r.Method == http.MethodGet:
		s.oidcCallback(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/logged-out" && r.Method == http.MethodGet:
		s.oidcLoggedOut(w, r)
		return true
	case r.URL.Path == "/api/v1/oidc/backchannel-logout" && r.Method == http.MethodPost:
		// Public (authenticated by the logout token's signature), no CSRF.
		// Session rows live behind the writer, so readers forward the POST.
		s.forwardWrite(w, r, s.oidcBackchannelLogout)
		return true
	}

	return false
}

// serveKeyRequestEndpoint routes the public side of agent key requests:
//
//	POST /api/v1/setup/<slug>/key-requests        open a request
//	GET  /api/v1/setup/<slug>/key-requests/<id>   poll it
//
// It returns true when the path belongs to this endpoint.
func (s *Server) serveKeyRequestEndpoint(w http.ResponseWriter, r *http.Request) bool {
	slug, id, ok := parseKeyRequestPath(r.URL.Path)
	if !ok {
		return false
	}
	create := id == "" && r.Method == http.MethodPost
	poll := id != "" && r.Method == http.MethodGet
	if !create && !poll {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	s.handleKeyRequest(w, r, slug, id)
	return true
}

// parseKeyRequestPath splits /api/v1/setup/<slug>/key-requests[/<id>]. The id is
// empty for the collection path.
func parseKeyRequestPath(path string) (slug, id string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/api/v1/setup/")
	if !ok {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[1] != "key-requests" {
		return "", "", false
	}
	if len(parts) == 3 {
		id = parts[2]
	}
	return parts[0], id, true
}
