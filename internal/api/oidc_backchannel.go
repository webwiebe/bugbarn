package api

import (
	"net/http"
)

// backchannelRateLimit caps back-channel logout attempts per IP per window
// (shared window duration with the login limiter). The endpoint is public, so
// the limiter keeps JWKS-verification work bounded under abuse.
const backchannelRateLimit = 60

// oidcBackchannelLogout implements OIDC Back-Channel Logout 1.0 (the RP side):
// iambarn POSTs a signed logout token when a user's session ends centrally
// (logout, admin revoke, suspension) and every matching local session dies
// within seconds. Public (server-to-server), rate-limited, no CSRF — the
// logout token's signature is the authentication. In reader mode the route is
// forwarded to the writer (see servePublicEndpoint).
func (s *Server) oidcBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	// Per spec §2.8 the response must not be cached.
	w.Header().Set("Cache-Control", "no-store")
	if s.oidc == nil || s.sessionStore == nil {
		http.Error(w, "oidc not configured", http.StatusNotFound)
		return
	}
	ip := s.clientIP(r)
	if s.overRateLimit(&s.backchannelLimiter, ip, backchannelRateLimit) {
		s.logger.Warn("oidc: rate-limited backchannel logout", "ip", ip)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := r.ParseForm(); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	raw := r.PostFormValue("logout_token")
	if raw == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	claims, err := s.oidc.VerifyLogoutToken(r.Context(), raw)
	if err != nil {
		s.logger.Warn("oidc: invalid logout token", "error", err)
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	// Prefer the precise session (sid); fall back to killing every session of
	// the subject when the token targets the whole user.
	var deleted int64
	if claims.SessionID != "" {
		deleted, err = s.sessionStore.DeleteBySID(r.Context(), claims.SessionID)
	} else {
		deleted, err = s.sessionStore.DeleteBySub(r.Context(), claims.Subject)
	}
	if err != nil {
		s.logger.Error("oidc: backchannel session delete failed", "error", err)
		http.Error(w, "session delete failed", http.StatusInternalServerError)
		return
	}
	s.logger.Info("oidc: backchannel logout processed",
		"sid", claims.SessionID, "sub", claims.Subject, "deleted", deleted)
	w.WriteHeader(http.StatusOK)
}
