package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// loginAttempt tracks the rate-limit window for a single IP.
type loginAttempt struct {
	count       int
	windowStart time.Time
}

const (
	loginRateLimit   = 10              // max login attempts per window
	setupRateLimit   = 20              // max setup-key requests per window
	loginRateWindow  = time.Minute     // window duration (shared)
	loginCleanupFreq = 5 * time.Minute // how often to purge stale entries
)

// overRateLimit records one hit for ip against the given limiter and reports
// whether the caller has now exceeded limit within the current window. Shared by
// the login and setup endpoints.
func (s *Server) overRateLimit(limiter *sync.Map, ip string, limit int) bool {
	now := time.Now()
	val, _ := limiter.LoadOrStore(ip, &loginAttempt{windowStart: now})
	attempt := val.(*loginAttempt)
	if now.Sub(attempt.windowStart) >= loginRateWindow {
		attempt.count = 0
		attempt.windowStart = now
	}
	attempt.count++
	return attempt.count > limit
}

// cleanupLoginLimiter periodically removes stale IP entries from the rate limiters.
func (s *Server) cleanupLoginLimiter(ctx context.Context) {
	ticker := time.NewTicker(loginCleanupFreq)
	defer ticker.Stop()
	purge := func(limiter *sync.Map, cutoff time.Time) {
		limiter.Range(func(key, value any) bool {
			if a, ok := value.(*loginAttempt); ok && a.windowStart.Before(cutoff) {
				limiter.Delete(key)
			}
			return true
		})
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-loginRateWindow)
			purge(&s.loginLimiter, cutoff)
			purge(&s.setupLimiter, cutoff)
		}
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.authEnabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}
	if s.users == nil || !s.users.Enabled() {
		// OIDC-only deployment: there are no local credentials to verify.
		// Fail closed instead of handing out a session.
		http.Error(w, "local login disabled", http.StatusUnauthorized)
		return
	}

	// Rate-limit by client IP.
	ip := s.clientIP(r)
	if s.overRateLimit(&s.loginLimiter, ip, loginRateLimit) {
		s.logger.Warn("auth: rate-limited login", "ip", ip)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}

	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&request); err != nil {
		http.Error(w, "invalid login payload", http.StatusBadRequest)
		return
	}
	if !s.users.Valid(request.Username, request.Password) {
		s.logger.Warn("auth: failed login", "username", request.Username, "ip", ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.sessions == nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	// Local admin logins get a web_sessions row too (empty token columns) so
	// one middleware and one revocation story cover both auth methods.
	handle, expires, err := s.createWebSession(r.Context(), storage.WebSession{
		Username:   s.users.Username(),
		AuthMethod: storage.WebSessionAuthLocal,
	})
	if err != nil {
		s.logger.Error("auth: create session failed", "error", err)
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := s.secureCookie(r)
	http.SetCookie(w, auth.SessionCookie(handle, expires, secure))
	http.SetCookie(w, s.sessions.CSRFCookie(handle, expires, secure))
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": s.users.Username()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	secure := s.secureCookie(r)
	logoutURL := ""
	if cookie, err := r.Cookie("bugbarn_session"); err == nil && cookie.Value != "" && s.sessionStore != nil {
		idHash := auth.HashSessionHandle(cookie.Value)
		if ws, gerr := s.sessionStore.Get(r.Context(), idHash); gerr == nil {
			if ws.AuthMethod == storage.WebSessionAuthOIDC && s.oidc != nil {
				// Best-effort server-side revocation: the refresh-token family
				// dies at iambarn instead of merely being forgotten locally.
				if rerr := s.oidc.RevokeRefreshToken(r.Context(), ws.RefreshToken); rerr != nil {
					s.logger.Warn("auth: logout revoke failed", "error", rerr)
				}
				// Server-driven RP-initiated logout: the SPA follows this URL
				// so the iambarn session ends too.
				logoutURL = s.oidc.LogoutURLWithIDTokenHint(ws.IDToken)
			}
			s.deleteSessionRow(r.Context(), idHash)
		}
	}
	http.SetCookie(w, auth.ClearSessionCookie(secure))
	http.SetCookie(w, auth.ClearCSRFCookie(secure))
	// Clear the auth-method hint set by the OIDC callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "bugbarn_auth_method",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"authenticated": false, "logout_url": logoutURL})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.authEnabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}
	ws, ok := s.resolveSession(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Refresh the CSRF cookie so the frontend always has a valid token.
	if sessionCookie, err := r.Cookie("bugbarn_session"); err == nil && s.sessions != nil {
		secure := s.secureCookie(r)
		expires := time.Now().Add(12 * time.Hour)
		http.SetCookie(w, s.sessions.CSRFCookie(sessionCookie.Value, expires, secure))
	}
	writeJSON(w, map[string]any{
		"authenticated": true,
		"authEnabled":   true,
		"username":      ws.Username,
		"authMethod":    ws.AuthMethod,
	})
}

// clientIP returns the real client IP. X-Forwarded-For is only trusted when
// the direct connection comes from a configured trusted proxy CIDR; otherwise
// RemoteAddr is used so callers cannot rotate their apparent IP to bypass
// the login rate limiter.
func (s *Server) clientIP(r *http.Request) string {
	remoteIP := remoteHost(r.RemoteAddr)
	if len(s.trustedProxies) > 0 && isTrustedProxy(remoteIP, s.trustedProxies) {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if idx := strings.Index(forwarded, ","); idx > 0 {
				return strings.TrimSpace(forwarded[:idx])
			}
			return strings.TrimSpace(forwarded)
		}
	}
	return remoteIP
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func isTrustedProxy(ip string, cidrs []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range cidrs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}
