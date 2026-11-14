package api

import (
	"context"
	"net/http"
)

// middleware wraps a handler. A route group lists its middleware outermost first.
type middleware func(http.Handler) http.Handler

// wildcardCORS answers any origin without credentials, for the browser ingest
// endpoints. The ingest-only key scope makes read access impossible.
func wildcardCORS(methods, headers string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setWildcardCORS(w, methods, headers)
			next.ServeHTTP(w, r)
		})
	}
}

// generalCORS applies the credentialed CORS policy (reflected allowed origin)
// and answers every preflight, whatever the path.
func (s *Server) generalCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.setCORSHeaders(w, r)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate enforces the session or API key, the key scope and CSRF, then
// resolves the request's project and group scope into its context. With no
// browser auth configured it passes everything through.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, ok := s.authenticateAndResolve(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// forwardWrites sends every non-GET request to the writer when this process is a
// reader (CQRS split). The writer authenticates it again.
func (s *Server) forwardWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.writeForwarder != nil && r.Method != http.MethodGet {
			s.writeForwarder.Forward(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// routeFlag is a per-route policy the authentication step reads from the
// request context. A route group sets it before authenticate runs.
type routeFlag int

const (
	// flagAdminOnly keeps read-scope API keys away from the route.
	flagAdminOnly routeFlag = 1 << iota
	// flagNoCSRF exempts the route from the double-submit CSRF check.
	flagNoCSRF
)

type routeFlagsKey struct{}

func withRouteFlag(flag routeFlag) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flags, _ := r.Context().Value(routeFlagsKey{}).(routeFlag)
			ctx := context.WithValue(r.Context(), routeFlagsKey{}, flags|flag)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func hasRouteFlag(r *http.Request, flag routeFlag) bool {
	flags, _ := r.Context().Value(routeFlagsKey{}).(routeFlag)
	return flags&flag != 0
}

// inIssueScope answers 404 for an issue of another project when the caller is
// confined to one. It reads the {id} wildcard of the route it wraps.
func (s *Server) inIssueScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.requireIssueInScope(w, r, r.PathValue("id")) {
			next(w, r)
		}
	}
}
