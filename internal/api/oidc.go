package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

const (
	oidcStateCookie  = "bugbarn_oidc_state"
	oidcNonceCookie  = "bugbarn_oidc_nonce"
	oidcReturnCookie = "bugbarn_oidc_return"
	oidcCookieTTL    = 10 * time.Minute
)

// sanitizeReturnTo constrains a caller-supplied post-login redirect to a safe,
// same-origin relative path so it can't be turned into an open redirect or a
// header-injection vector. Anything that isn't a plain "/…" path (absolute URLs,
// protocol-relative "//host", CR/LF, or over-long values) collapses to "/".
func sanitizeReturnTo(raw string) string {
	if raw == "" || len(raw) > 512 {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "\r\n") {
		return "/"
	}
	return raw
}

// readReturnTo recovers the post-login redirect stashed by oidcLogin, defaulting
// to "/" and re-sanitizing defensively.
func (s *Server) readReturnTo(r *http.Request) string {
	c, err := r.Cookie(oidcReturnCookie)
	if err != nil || c.Value == "" {
		return "/"
	}
	decoded, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return "/"
	}
	return sanitizeReturnTo(string(decoded))
}

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
	case "none":
		// Silent re-auth for the widget "reconnect" flow: iambarn issues a code
		// without UI when the SSO session is still alive, or returns
		// error=login_required — which the callback escalates to an interactive
		// login so the user's on-demand reconnect still completes.
		prompt = "none"
	}
	returnTo := sanitizeReturnTo(r.URL.Query().Get("return_to"))
	authURL, err := s.oidc.AuthorizeURL(state, nonce, prompt)
	if err != nil {
		s.logger.Warn("oidc: build authorize url", "error", err)
		http.Error(w, "oidc unavailable", http.StatusServiceUnavailable)
		return
	}
	secure := secureCookie(r)
	http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, state, secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, nonce, secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcReturnCookie,
		base64.RawURLEncoding.EncodeToString([]byte(returnTo)), secure))
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
	// A prompt=none silent re-auth that found no live IAMBarn session comes back
	// with an error and no code. For the interaction-required family, escalate to
	// an interactive login (preserving return_to) so the reconnect completes; any
	// other error returns the user to the app root with a marker.
	if oidcErr := r.URL.Query().Get("error"); oidcErr != "" {
		secure := secureCookie(r)
		http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, "", secure))
		http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, "", secure))
		switch oidcErr {
		case "login_required", "interaction_required", "consent_required", "account_selection_required":
			q := url.Values{}
			if rt := s.readReturnTo(r); rt != "/" {
				q.Set("return_to", rt)
			}
			http.SetCookie(w, oidcShortLivedCookie(oidcReturnCookie, "", secure))
			http.Redirect(w, r, "/api/v1/oidc/login?"+q.Encode(), http.StatusFound)
		default:
			http.SetCookie(w, oidcShortLivedCookie(oidcReturnCookie, "", secure))
			http.Redirect(w, r, "/?oidc_error="+url.QueryEscape(oidcErr), http.StatusFound)
		}
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
		http.SetCookie(w, oidcShortLivedCookie(oidcReturnCookie, "", secure))
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
		Name:    "bugbarn_auth_method",
		Value:   "oidc",
		Path:    "/",
		Expires: expires,
		Secure:  secure,
		// Strict matches the session/CSRF cookies. The short-lived OIDC
		// state/nonce cookies below intentionally stay Lax because they must
		// survive the cross-site top-level redirect back from the IdP.
		SameSite: http.SameSiteStrictMode,
	})
	// Return the browser where it started the flow (e.g. #/account for a widget
	// reconnect), defaulting to the app root.
	returnTo := s.readReturnTo(r)
	// Clear the short-lived state/nonce/return cookies.
	http.SetCookie(w, oidcShortLivedCookie(oidcStateCookie, "", secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcNonceCookie, "", secure))
	http.SetCookie(w, oidcShortLivedCookie(oidcReturnCookie, "", secure))
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// oidcLoggedOut is the landing endpoint for the IdP's RP-initiated logout
// redirect (the post_logout_redirect_uri registered on the OIDC client and
// handed to the hosted IAMBarn widgets). The hosted "Log out" ends the IAMBarn
// session but never touches this barn's own cookies, so we clear the local
// session/CSRF/auth-method cookies here before returning the user to the app.
// No auth is required — the session may already be gone.
func (s *Server) oidcLoggedOut(w http.ResponseWriter, r *http.Request) {
	secure := secureCookie(r)
	http.SetCookie(w, auth.ClearSessionCookie(secure))
	http.SetCookie(w, auth.ClearCSRFCookie(secure))
	// Clear the auth-method hint set by the OIDC callback (mirrors logout()).
	http.SetCookie(w, &http.Cookie{
		Name:     "bugbarn_auth_method",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
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
