package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// TokenRefresher is the slice of *auth.OIDCClient the Direct store needs to
// renew and revoke sessions. Narrow so tests can script token-endpoint
// behavior (rotation, invalid_grant, 5xx) without a fake IdP.
type TokenRefresher interface {
	Refresh(ctx context.Context, refreshToken string) (auth.RefreshedTokens, error)
	Allowed(claims auth.Claims) bool
}

// Direct is the SQLite-backed Store used in single-process mode and on the
// writer. An in-process singleflight serializes refreshes per session; because
// the writer is a single replica, that flight is effectively global, so the
// single-use refresh token is never sent twice.
type Direct struct {
	db   *storage.Store
	oidc TokenRefresher
	sf   singleflight.Group
	now  func() time.Time
}

// NewDirect builds a Direct store over the given storage. oidc may be nil when
// OIDC is not configured (local-admin sessions never refresh).
func NewDirect(db *storage.Store, oidc TokenRefresher) *Direct {
	return &Direct{db: db, oidc: oidc, now: time.Now}
}

// SetRefresher wires the OIDC client after construction (server wiring sets
// the session store before it knows whether OIDC is configured).
func (d *Direct) SetRefresher(oidc TokenRefresher) { d.oidc = oidc }

// Create persists a new session row.
func (d *Direct) Create(ctx context.Context, ws storage.WebSession) error {
	return d.db.InsertWebSession(ctx, ws)
}

// Get loads a session row by handle hash.
func (d *Direct) Get(ctx context.Context, idHash string) (storage.WebSession, error) {
	ws, err := d.db.GetWebSession(ctx, idHash)
	if errors.Is(err, apperr.ErrNotFound) {
		return storage.WebSession{}, ErrNotFound
	}
	return ws, err
}

// Delete removes a session row.
func (d *Direct) Delete(ctx context.Context, idHash string) error {
	return d.db.DeleteWebSession(ctx, idHash)
}

// DeleteBySID removes all sessions bound to an IdP session id.
func (d *Direct) DeleteBySID(ctx context.Context, sid string) (int64, error) {
	return d.db.DeleteWebSessionsBySID(ctx, sid)
}

// DeleteBySub removes all sessions for an IdP subject.
func (d *Direct) DeleteBySub(ctx context.Context, sub string) (int64, error) {
	return d.db.DeleteWebSessionsBySub(ctx, sub)
}

// NeedsRefresh reports whether the session's access token is within the skew
// window of expiry (or past it). Local sessions never refresh.
func NeedsRefresh(ws storage.WebSession, now time.Time) bool {
	if ws.AuthMethod != storage.WebSessionAuthOIDC {
		return false
	}
	if ws.AccessExpiresAt.IsZero() {
		return false
	}
	return !now.Before(ws.AccessExpiresAt.Add(-accessSkew * time.Second))
}

// refreshResult carries a session + sticky error pair through singleflight.
type refreshResult struct {
	ws  storage.WebSession
	err error
}

// Refresh renews the session's tokens if needed. Concurrent callers for the
// same session share one flight so the single-use refresh token is used once.
func (d *Direct) Refresh(ctx context.Context, idHash string) (storage.WebSession, error) {
	// Detach from the caller's cancellation: if the browser disconnects after
	// the IdP has rotated the refresh token but before we stored it, the
	// session would be stranded with a dead token. The OIDC client applies its
	// own timeout, so the flight stays bounded.
	flightCtx := context.WithoutCancel(ctx)
	v, _, _ := d.sf.Do(idHash, func() (any, error) {
		ws, err := d.refreshLocked(flightCtx, idHash)
		return refreshResult{ws: ws, err: err}, nil
	})
	res := v.(refreshResult)
	return res.ws, res.err
}

// refreshLocked runs under the per-session singleflight.
func (d *Direct) refreshLocked(ctx context.Context, idHash string) (storage.WebSession, error) {
	ws, err := d.Get(ctx, idHash)
	if err != nil {
		return storage.WebSession{}, err
	}
	now := d.now().UTC()
	if !NeedsRefresh(ws, now) {
		// Another flight (or request) refreshed moments ago — serve its result.
		return ws, nil
	}
	if d.oidc == nil || ws.RefreshToken == "" {
		// An OIDC session that cannot be renewed is dead once its access token
		// expires: fail closed rather than serving an unverifiable session.
		_ = d.db.DeleteWebSession(ctx, idHash)
		return storage.WebSession{}, ErrRevoked
	}

	refreshed, err := d.oidc.Refresh(ctx, ws.RefreshToken)
	if errors.Is(err, auth.ErrRefreshInvalid) {
		// The IdP killed this grant (revocation, suspension, rotation replay).
		// The session dies with it, immediately and without grace.
		_ = d.db.DeleteWebSession(ctx, idHash)
		return storage.WebSession{}, ErrRevoked
	}
	if err != nil {
		// Transient (network/5xx): record when the outage started so the
		// bounded-grace policy has a fixed anchor, and serve the stale row.
		_ = d.db.MarkWebSessionRefreshFailing(ctx, idHash, now)
		if ws.RefreshFailingSince.IsZero() {
			ws.RefreshFailingSince = now
		}
		return ws, fmt.Errorf("%w: %w", ErrTransient, err)
	}
	return d.applyRefreshedTokens(ctx, ws, refreshed, now)
}

// applyRefreshedTokens persists a successful refresh: rotated tokens, new
// expiry, and — when the response carried a fresh id_token — a re-snapshot of
// identity and groups/roles (losing access centrally revokes the session).
func (d *Direct) applyRefreshedTokens(
	ctx context.Context, ws storage.WebSession, refreshed auth.RefreshedTokens, now time.Time,
) (storage.WebSession, error) {
	ws.AccessToken = refreshed.AccessToken
	ws.RefreshToken = refreshed.RefreshToken
	ws.AccessExpiresAt = refreshed.ExpiresAt.UTC()
	ws.LastRefreshAt = now
	ws.RefreshFailingSince = time.Time{}
	if refreshed.Claims != nil {
		if !d.oidc.Allowed(*refreshed.Claims) {
			_ = d.db.DeleteWebSession(ctx, ws.IDHash)
			return storage.WebSession{}, ErrRevoked
		}
		ws.IDToken = refreshed.IDToken
		if name := refreshed.Claims.PreferredName(); name != "" {
			ws.Username = name
		}
		if snapshot, jerr := json.Marshal(refreshed.Claims); jerr == nil {
			ws.ClaimsJSON = string(snapshot)
		}
	}
	if err := d.db.UpdateWebSessionTokens(ctx, ws); err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Deleted underneath us (back-channel logout won the race).
			return storage.WebSession{}, ErrRevoked
		}
		return storage.WebSession{}, err
	}
	return ws, nil
}
