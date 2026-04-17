package api

import (
	"encoding/json"
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
	ip := clientIP(r)
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
	http.SetCookie(w, auth.CSRFCookie(token, expires, secure))
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

// clientIP extracts the best-effort client IP from the request.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// Take the first (leftmost) address — the original client.
		if idx := strings.Index(forwarded, ","); idx > 0 {
			return strings.TrimSpace(forwarded[:idx])
		}
		return strings.TrimSpace(forwarded)
	}
	// RemoteAddr is "ip:port".
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
