package api

import (
	"net/http"
	"sort"
	"strings"
)

// Routing lives in the routes*.go files and nowhere else. Every route is one
// pattern registered on one http.ServeMux ("METHOD /path/{wildcard}"), and a
// handler reads its wildcards with r.PathValue. Each auth class keeps its
// registrations in its own file:
//
//	routes_ingest.go     key-authenticated ingest endpoints with wildcard CORS
//	routes_public.go     public endpoints (health, login, OIDC, setup)
//	routes_internal.go   HMAC-signed writer-internal endpoints
//	routes_protected.go  endpoints behind the session or API key pipeline
//	routes_middleware.go the per-group middleware (CORS, auth, forwarding)
//
// Feature files hold handlers only: no handler parses the path or re-checks the
// method, because a pattern that matched already did both.
//
// Unmatched requests. A method-less pattern, "/", sits behind the protected
// middleware, so an unauthenticated request that matches no route (an unknown
// path, or a known path with the wrong method) answers 401 like every protected
// route, and an authenticated one answers 404 or 405 with an Allow header. The
// ingest, check-in and internal endpoints answer a wrong method with 405 before
// authentication, as they always did (rejectMethods). Routing errors are plain
// text, the shape http.Error and http.NotFound give; the API has never returned
// JSON for an unknown path.

// router is the one ServeMux that serves the API, plus the patterns registered
// on it. ServeMux cannot list its patterns, and the OpenAPI parity test and the
// protected-route scope test read them from here. operations are the routes the
// API offers; fallbacks answer what no operation matches (preflights, wrong
// methods, trailing-slash twins of an operation) and are not API surface.
type router struct {
	mux        *http.ServeMux
	operations []string
	fallbacks  []string
}

func (rt *router) handle(pattern string, h http.Handler, fallback bool) {
	rt.mux.Handle(pattern, h)
	if fallback {
		rt.fallbacks = append(rt.fallbacks, pattern)
		return
	}
	rt.operations = append(rt.operations, pattern)
}

// group is a set of middleware applied to every route registered through it,
// outermost first.
type group struct {
	rt  *router
	mws []middleware
}

func (rt *router) group(mws ...middleware) group { return group{rt: rt, mws: mws} }

// handle registers an operation of the API.
func (g group) handle(pattern string, h http.HandlerFunc) { g.register(pattern, h, false) }

// fallback registers a route that is no API operation: a preflight, a wrong
// method answer, or a trailing-slash twin of an operation.
func (g group) fallback(pattern string, h http.HandlerFunc) { g.register(pattern, h, true) }

func (g group) register(pattern string, h http.HandlerFunc, fallback bool) {
	handler := http.Handler(h)
	for i := len(g.mws) - 1; i >= 0; i-- {
		handler = g.mws[i](handler)
	}
	g.rt.handle(pattern, handler, fallback)
}

// router returns the server's route table, building it on first use so the
// Set* configuration methods may run after NewServer.
func (s *Server) router() *router {
	s.routesOnce.Do(func() { s.routes = s.buildRouter() })
	return s.routes
}

func (s *Server) buildRouter() *router {
	rt := &router{mux: http.NewServeMux()}
	s.registerIngestRoutes(rt)
	s.registerPublicRoutes(rt)
	s.registerInternalRoutes(rt)
	s.registerProtectedRoutes(rt)
	return rt
}

// routePatterns lists the pattern of every API operation, sorted.
func (s *Server) routePatterns() []string {
	out := append([]string(nil), s.router().operations...)
	sort.Strings(out)
	return out
}

// rejectMethods answers 405 with an Allow header to the given methods of a path,
// ahead of any authentication. An endpoint with its own method set registers it
// next to its handlers, so a wrong method never falls through to the protected
// pipeline.
func rejectMethods(g group, path, allow string, methods ...string) {
	for _, m := range methods {
		g.fallback(m+" "+path, methodNotAllowed(allow))
	}
}

func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if allow != "" {
			w.Header().Set("Allow", allow)
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// preflight answers a CORS preflight; the group's CORS middleware set the headers.
func preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// unmatched is the handler behind the "/" pattern. By the time it runs the
// request is authenticated, so it can say whether the path exists under another
// method (405 with Allow) or nowhere (404).
func (s *Server) unmatched(w http.ResponseWriter, r *http.Request) {
	if allow := s.router().allowedMethods(r); len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.NotFound(w, r)
}

// allowedMethods lists the methods some registered pattern accepts for the
// request's path. HEAD follows GET, as it does in ServeMux.
func (rt *router) allowedMethods(r *http.Request) []string {
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := rt.mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, m)
			if m == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
	}
	return allow
}
