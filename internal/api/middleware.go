package api

import (
	"net/http"
	"strings"
)

// setCORSHeaders applies CORS policy. We never use * because BugBarn uses
// cookies (credentials), which require an explicit reflected origin.
//
//   - If BUGBARN_ALLOWED_ORIGINS is set (parsed into s.allowedOrigins), any
//     request whose Origin matches the list is reflected with credentials.
//   - Otherwise we only reflect the origin when it is same-origin (Origin
//     host == Host header), so localhost dev still works without configuration.
//   - Vary: Origin is always emitted so CDNs don't cache the wrong value.
func (s *Server) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Headers", "content-type, x-bugbarn-api-key, x-bugbarn-csrf, x-bugbarn-project")

	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	if len(s.allowedOrigins) > 0 {
		for _, allowed := range s.allowedOrigins {
			if strings.EqualFold(origin, allowed) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				return
			}
		}
		// Origin not in the explicit list — don't set ACAO.
		return
	}

	// No explicit list: allow same-origin only.
	if sameOrigin(origin, r.Host) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// sameOrigin returns true when the request Origin matches the Host header,
// i.e. the browser and the API server are on the same host+port.
func sameOrigin(origin, host string) bool {
	// origin is a full URL like "http://localhost:8080"; strip the scheme.
	stripped := origin
	if i := strings.Index(stripped, "://"); i >= 0 {
		stripped = stripped[i+3:]
	}
	// Remove any trailing slash.
	stripped = strings.TrimRight(stripped, "/")
	// host may or may not include port; compare case-insensitively.
	return strings.EqualFold(stripped, host)
}

// isCSRFProtected returns true for state-changing requests that are NOT the
// ingest endpoint (which uses API key auth) and NOT login/logout.
// isIssueAction returns true for issue mutation endpoints that operate by
// numeric row ID (resolve/reopen/mute/unmute). These don't need project
// scoping because the row ID is globally unique across all projects.
func isIssueAction(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/v1/issues/") && (
		strings.HasSuffix(p, "/resolve") ||
		strings.HasSuffix(p, "/reopen") ||
		strings.HasSuffix(p, "/mute") ||
		strings.HasSuffix(p, "/unmute"))
}

func isCSRFProtected(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
		// Ingest and login/logout are excluded.
		switch r.URL.Path {
		case "/api/v1/events", "/api/v1/login", "/api/v1/logout":
			return false
		}
		return true
	}
	return false
}

// validCSRF checks the double-submit cookie pattern: the X-BugBarn-CSRF
// header must match the CSRF token derived from the session cookie.
func (s *Server) validCSRF(r *http.Request) bool {
	sessionCookie, err := r.Cookie("bugbarn_session")
	if err != nil {
		return false
	}
	expected := s.sessions.CSRFToken(sessionCookie.Value)
	provided := r.Header.Get("X-BugBarn-CSRF")
	return provided == expected
}
