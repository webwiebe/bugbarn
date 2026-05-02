package api

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

// loginAttempt tracks the rate-limit window for a single IP.
type loginAttempt struct {
	count       int
	windowStart time.Time
}

const (
	loginRateLimit   = 10              // max attempts per window
	loginRateWindow  = time.Minute     // window duration
	loginCleanupFreq = 5 * time.Minute // how often to purge stale entries
)

// cleanupLoginLimiter periodically removes stale IP entries from the login limiter.
func (s *Server) cleanupLoginLimiter() {
	ticker := time.NewTicker(loginCleanupFreq)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-loginRateWindow)
		s.loginLimiter.Range(func(key, value any) bool {
			if a, ok := value.(*loginAttempt); ok && a.windowStart.Before(cutoff) {
				s.loginLimiter.Delete(key)
			}
			return true
		})
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.users == nil || !s.users.Enabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}

	// Rate-limit by client IP.
	ip := s.clientIP(r)
	now := time.Now()
	val, _ := s.loginLimiter.LoadOrStore(ip, &loginAttempt{windowStart: now})
	attempt := val.(*loginAttempt)
	if now.Sub(attempt.windowStart) >= loginRateWindow {
		// Window has expired; reset.
		attempt.count = 0
		attempt.windowStart = now
	}
	attempt.count++
	if attempt.count > loginRateLimit {
		log.Printf("auth: rate-limited login from %s (attempt %d)", ip, attempt.count)
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
		log.Printf("auth: failed login for user %q from %s", request.Username, ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.sessions == nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	token, expires, err := s.sessions.Create(s.users.Username())
	if err != nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := secureCookie(r)
	http.SetCookie(w, auth.SessionCookie(token, expires, secure))
	http.SetCookie(w, s.sessions.CSRFCookie(token, expires, secure))
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": s.users.Username()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	secure := secureCookie(r)
	http.SetCookie(w, auth.ClearSessionCookie(secure))
	http.SetCookie(w, auth.ClearCSRFCookie(secure))
	writeJSON(w, map[string]any{"authenticated": false})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.users == nil || !s.users.Enabled() {
		writeJSON(w, map[string]any{"authenticated": true, "authEnabled": false})
		return
	}
	username, ok := s.sessionUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Refresh the CSRF cookie so the frontend always has a valid token.
	if sessionCookie, err := r.Cookie("bugbarn_session"); err == nil && s.sessions != nil {
		secure := secureCookie(r)
		expires := time.Now().Add(12 * time.Hour)
		http.SetCookie(w, s.sessions.CSRFCookie(sessionCookie.Value, expires, secure))
	}
	writeJSON(w, map[string]any{"authenticated": true, "authEnabled": true, "username": username})
}

func (s *Server) sessionUser(r *http.Request) (string, bool) {
	if s == nil || s.sessions == nil {
		return "", false
	}
	cookie, err := r.Cookie("bugbarn_session")
	if err != nil {
		return "", false
	}
	return s.sessions.Valid(cookie.Value)
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

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
