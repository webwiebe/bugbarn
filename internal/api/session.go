package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// authEnabled reports whether any browser-auth mechanism is configured. The
// auth gate must engage when EITHER local users OR OIDC is set up — gating on
// local users alone left an OIDC-only deployment wide open.
func (s *Server) authEnabled() bool {
	if s == nil {
		return false
	}
	return (s.users != nil && s.users.Enabled()) || s.oidc != nil
}

// sessionUser resolves the request's session and returns its username.
func (s *Server) sessionUser(r *http.Request) (string, bool) {
	ws, ok := s.resolveSession(r)
	return ws.Username, ok
}

// resolveSession authenticates a request against the server-side session
// store: load the row by cookie-handle hash, enforce the absolute lifetime
// cap, and — for OIDC sessions whose access token is (nearly) expired —
// refresh the tokens through the store. invalid_grant kills the session
// immediately; IdP outages get bounded grace on the stale session.
func (s *Server) resolveSession(r *http.Request) (storage.WebSession, bool) {
	if s == nil || s.sessionStore == nil {
		return storage.WebSession{}, false
	}
	cookie, err := r.Cookie("bugbarn_session")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return storage.WebSession{}, false
	}
	ctx := r.Context()
	idHash := auth.HashSessionHandle(cookie.Value)
	ws, err := s.sessionStore.Get(ctx, idHash)
	if err != nil {
		return storage.WebSession{}, false
	}
	now := time.Now().UTC()
	if !ws.AbsoluteExpiresAt.IsZero() && now.After(ws.AbsoluteExpiresAt) {
		s.deleteSessionRow(ctx, idHash)
		return storage.WebSession{}, false
	}
	if !sessionstore.NeedsRefresh(ws, now) {
		return ws, true
	}
	refreshed, rerr := s.sessionStore.Refresh(ctx, idHash)
	switch {
	case rerr == nil:
		return refreshed, true
	case errors.Is(rerr, sessionstore.ErrRevoked), errors.Is(rerr, sessionstore.ErrNotFound):
		s.logger.Info("session: revoked by IdP", "username", ws.Username)
		return storage.WebSession{}, false
	default:
		return s.staleSessionWithinGrace(ws, refreshed, rerr, now)
	}
}

// staleSessionWithinGrace applies the bounded-grace outage policy: a session
// whose refresh keeps failing transiently (IdP down, writer unreachable) is
// served stale until the grace ceiling, anchored at the first failure.
func (s *Server) staleSessionWithinGrace(ws, refreshed storage.WebSession, rerr error, now time.Time) (storage.WebSession, bool) {
	stale := ws
	if refreshed.IDHash != "" {
		stale = refreshed
	}
	anchor := stale.RefreshFailingSince
	if anchor.IsZero() {
		anchor = stale.AccessExpiresAt
	}
	if !anchor.IsZero() && now.Sub(anchor) > s.refreshGrace() {
		s.logger.Warn("session: refresh grace exceeded", "username", stale.Username, "error", rerr)
		return storage.WebSession{}, false
	}
	s.logger.Warn("session: serving stale session during refresh outage", "username", stale.Username, "error", rerr)
	return stale, true
}

// refreshGrace returns the bounded-grace ceiling for transient refresh
// failures (default 1h).
func (s *Server) refreshGrace() time.Duration {
	if s.oidcRefreshGrace > 0 {
		return s.oidcRefreshGrace
	}
	return time.Hour
}

// deleteSessionRow removes a session row best-effort (readers delegate to the
// writer; a failure only means the row lingers until pruning).
func (s *Server) deleteSessionRow(ctx context.Context, idHash string) {
	if err := s.sessionStore.Delete(ctx, idHash); err != nil {
		s.logger.Warn("session: delete failed", "error", err)
	}
}

// createWebSession mints an opaque handle, fills in the lifecycle columns,
// and persists the row (via the writer in reader mode). Returns the handle
// for the cookie and the absolute expiry.
func (s *Server) createWebSession(ctx context.Context, ws storage.WebSession) (string, time.Time, error) {
	if s.sessionStore == nil {
		return "", time.Time{}, errors.New("session store unavailable")
	}
	handle := auth.NewSessionHandle()
	now := time.Now().UTC()
	ws.IDHash = auth.HashSessionHandle(handle)
	ws.CreatedAt = now
	ws.AbsoluteExpiresAt = now.Add(s.sessions.TTL())
	if err := s.sessionStore.Create(ctx, ws); err != nil {
		return "", time.Time{}, err
	}
	return handle, ws.AbsoluteExpiresAt, nil
}

// secureCookie decides the Secure flag on issued cookies. TLS on the direct
// connection always wins; X-Forwarded-Proto is only honored when the direct
// peer is a configured trusted proxy (BUGBARN_TRUSTED_PROXIES) so a spoofed
// header can't downgrade or fake the scheme. Outside local dev (any non-empty
// BUGBARN_ENVIRONMENT) the default is Secure=true.
func (s *Server) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if len(s.trustedProxies) > 0 && isTrustedProxy(remoteHost(r.RemoteAddr), s.trustedProxies) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return s.environment != ""
}
