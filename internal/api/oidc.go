package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

const (
	oidcStateCookie = "bugbarn_oidc_state"
	oidcNonceCookie = "bugbarn_oidc_nonce"
	oidcCookieTTL   = 10 * time.Minute
)

// oidcLogin starts the OIDC authorization-code flow by redirecting the browser
// to the iambarn authorize endpoint. State + nonce are stored in short-lived,
// HttpOnly cookies and checked on the callback.
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Error(w, "oidc not configured", http.StatusNotFound)
		return
	}
	state := randomToken()
	nonce := randomToken()
	// Allow the SPA to ask iambarn to re-prompt (e.g. after we rejected the
	// previous identity for not having access). Only the two standard OIDC
	// values are accepted.
	prompt := ""
	switch r.URL.Query().Get("prompt") {
	case "login":
		prompt = "login"
	case "select_account":
		prompt = "select_account"
	}
	authURL, err := s.oidc.AuthorizeURL(state, nonce, prompt)
	if err != nil {
		s.logger.Warn("oidc: build authorize url", "error", err)
		http.Error(w, "oidc unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := secureCookie(r)
	http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, state, secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, nonce, secure))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oidcCallback handles the redirect back from iambarn. On success it issues a
// local session cookie that authenticates the browser for the SPA.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Error(w, "oidc not configured", http.StatusNotFound)
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		s.logger.Warn("oidc: state mismatch")
		http.Error(w, "oidc state mismatch", http.StatusBadRequest)
		return
	}
	nonceCookie, err := r.Cookie(oidcNonceCookie)
	if err != nil || nonceCookie.Value == "" {
		http.Error(w, "oidc nonce missing", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "oidc code missing", http.StatusBadRequest)
		return
	}
	claims, err := s.oidc.Exchange(r.Context(), code, nonceCookie.Value)
	if err != nil {
		s.logger.Warn("oidc: exchange failed", "error", err)
		http.Error(w, "oidc exchange failed", http.StatusUnauthorized)
		return
	}
	if !s.oidc.Allowed(claims) {
		s.logger.Warn("oidc: access denied",
			"sub", claims.Subject, "groups", claims.Groups, "roles", claims.Roles)
		// Clear the short-lived state/nonce cookies before redirecting so the
		// SPA's "Switch account" button starts a clean flow.
		secure := secureCookie(r)
		http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, "", secure))
		http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, "", secure))
		q := url.Values{}
		q.Set("oidc_error", "access_denied")
		if id := claims.PreferredName(); id != "" {
			q.Set("identity", id)
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusFound)
		return
	}
	if s.sessions == nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	username := claims.PreferredName()
	if username == "" {
		username = "oidc-user"
	}
	token, expires, err := s.sessions.Create(username)
	if err != nil {
		http.Error(w, "session unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := secureCookie(r)
	http.SetCookie(w, auth.SessionCookie(token, expires, secure))
	http.SetCookie(w, s.sessions.CSRFCookie(token, expires, secure))
	// Non-HttpOnly hint so the SPA can show OIDC-specific UI (e.g. the
	// IAMBarn profile link) only for sessions that actually came from
	// iambarn. Same expiry as the session.
	http.SetCookie(w, &http.Cookie{
		Name:     "bugbarn_auth_method",
		Value:    "oidc",
		Path:     "/",
		Expires:  expires,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	// Clear the short-lived state/nonce cookies.
	http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, "", secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, "", secure))
	http.Redirect(w, r, "/", http.StatusFound)
}

func oidcShortLivedCookie(name, value string, secure bool) *http.Cookie {
	maxAge := int(oidcCookieTTL.Seconds())
	if value == "" {
		maxAge = -1
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

func randomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}
