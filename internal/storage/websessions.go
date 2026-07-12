package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// WebSession is one server-side browser session row. The browser only holds an
// opaque random handle; IDHash is the SHA-256 hex digest of that handle. For
// OIDC sessions the iambarn tokens live here (never in the browser); local
// admin sessions have empty token columns.
type WebSession struct {
	IDHash              string    `json:"id_hash"`
	Username            string    `json:"username"`
	AuthMethod          string    `json:"auth_method"` // "oidc" or "local"
	IdpSub              string    `json:"idp_sub"`
	IdpSid              string    `json:"idp_sid"`
	IDToken             string    `json:"id_token"`
	AccessToken         string    `json:"access_token"`
	RefreshToken        string    `json:"refresh_token"`
	AccessExpiresAt     time.Time `json:"access_expires_at"`
	ClaimsJSON          string    `json:"claims_json"`
	CreatedAt           time.Time `json:"created_at"`
	AbsoluteExpiresAt   time.Time `json:"absolute_expires_at"`
	LastRefreshAt       time.Time `json:"last_refresh_at"`
	RefreshFailingSince time.Time `json:"refresh_failing_since"`
}

// Auth methods for web sessions.
const (
	WebSessionAuthOIDC  = "oidc"
	WebSessionAuthLocal = "local"
)

const webSessionColumns = `id_hash, username, auth_method, idp_sub, idp_sid,
	id_token, access_token, refresh_token, access_expires_at, claims_json,
	created_at, absolute_expires_at, last_refresh_at, refresh_failing_since`

// InsertWebSession persists a new session row.
func (s *WebSessionStore) InsertWebSession(ctx context.Context, ws WebSession) error {
	if s == nil || s.db == nil {
		return apperr.Internal("storage is read-only", errors.New("no write connection"))
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO web_sessions (`+webSessionColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ws.IDHash, ws.Username, ws.AuthMethod, ws.IdpSub, ws.IdpSid,
		ws.IDToken, ws.AccessToken, ws.RefreshToken, formatTimeOrEmpty(ws.AccessExpiresAt), ws.ClaimsJSON,
		formatTime(ws.CreatedAt), formatTime(ws.AbsoluteExpiresAt),
		formatTimeOrEmpty(ws.LastRefreshAt), formatTimeOrEmpty(ws.RefreshFailingSince),
	)
	if err != nil {
		return apperr.Internal("insert web session", err)
	}
	return nil
}

// GetWebSession loads a session row by handle hash. Works on read-only stores.
func (s *WebSessionStore) GetWebSession(ctx context.Context, idHash string) (WebSession, error) {
	row := s.readDB().QueryRowContext(ctx,
		`SELECT `+webSessionColumns+` FROM web_sessions WHERE id_hash = ?`, idHash)
	var ws WebSession
	var accessExp, createdAt, absoluteExp, lastRefresh, failingSince string
	err := row.Scan(&ws.IDHash, &ws.Username, &ws.AuthMethod, &ws.IdpSub, &ws.IdpSid,
		&ws.IDToken, &ws.AccessToken, &ws.RefreshToken, &accessExp, &ws.ClaimsJSON,
		&createdAt, &absoluteExp, &lastRefresh, &failingSince)
	if errors.Is(err, sql.ErrNoRows) {
		return WebSession{}, apperr.NotFound("web session not found", nil)
	}
	if err != nil {
		return WebSession{}, apperr.Internal("get web session", err)
	}
	ws.AccessExpiresAt, _ = parseTime(accessExp)
	ws.CreatedAt, _ = parseTime(createdAt)
	ws.AbsoluteExpiresAt, _ = parseTime(absoluteExp)
	ws.LastRefreshAt, _ = parseTime(lastRefresh)
	ws.RefreshFailingSince, _ = parseTime(failingSince)
	return ws, nil
}

// UpdateWebSessionTokens stores the outcome of a successful refresh: new token
// material, expiry, optionally refreshed identity claims, and clears any
// refresh-failure marker.
func (s *WebSessionStore) UpdateWebSessionTokens(ctx context.Context, ws WebSession) error {
	if s == nil || s.db == nil {
		return apperr.Internal("storage is read-only", errors.New("no write connection"))
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE web_sessions SET
			username = ?, id_token = ?, access_token = ?, refresh_token = ?,
			access_expires_at = ?, claims_json = ?, last_refresh_at = ?,
			refresh_failing_since = ''
		 WHERE id_hash = ?`,
		ws.Username, ws.IDToken, ws.AccessToken, ws.RefreshToken,
		formatTimeOrEmpty(ws.AccessExpiresAt), ws.ClaimsJSON, formatTimeOrEmpty(ws.LastRefreshAt),
		ws.IDHash,
	)
	if err != nil {
		return apperr.Internal("update web session", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperr.NotFound("web session not found", nil)
	}
	return nil
}

// MarkWebSessionRefreshFailing records the start of a refresh outage for the
// bounded-grace policy. Only the FIRST failure sets the timestamp; later
// failures keep the original so the grace window can't be extended by retries.
func (s *WebSessionStore) MarkWebSessionRefreshFailing(ctx context.Context, idHash string, since time.Time) error {
	if s == nil || s.db == nil {
		return apperr.Internal("storage is read-only", errors.New("no write connection"))
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE web_sessions SET refresh_failing_since = ?
		 WHERE id_hash = ? AND refresh_failing_since = ''`,
		formatTimeOrEmpty(since), idHash,
	)
	if err != nil {
		return apperr.Internal("mark web session refresh failing", err)
	}
	return nil
}

// DeleteWebSession removes one session row by handle hash.
func (s *WebSessionStore) DeleteWebSession(ctx context.Context, idHash string) error {
	if s == nil || s.db == nil {
		return apperr.Internal("storage is read-only", errors.New("no write connection"))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE id_hash = ?`, idHash); err != nil {
		return apperr.Internal("delete web session", err)
	}
	return nil
}

// DeleteWebSessionsBySID deletes every session bound to an IdP session id
// (back-channel logout by sid). Returns the number of rows removed.
func (s *WebSessionStore) DeleteWebSessionsBySID(ctx context.Context, sid string) (int64, error) {
	return s.deleteWebSessionsWhere(ctx, `idp_sid = ?`, sid)
}

// DeleteWebSessionsBySub deletes every session for an IdP subject
// (back-channel logout by sub). Returns the number of rows removed.
func (s *WebSessionStore) DeleteWebSessionsBySub(ctx context.Context, sub string) (int64, error) {
	return s.deleteWebSessionsWhere(ctx, `idp_sub = ?`, sub)
}

// PruneWebSessions removes sessions past their absolute expiry.
func (s *WebSessionStore) PruneWebSessions(ctx context.Context, now time.Time) (int64, error) {
	return s.deleteWebSessionsWhere(ctx, `absolute_expires_at < ?`, formatTime(now))
}

func (s *WebSessionStore) deleteWebSessionsWhere(ctx context.Context, where string, arg string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, apperr.Internal("storage is read-only", errors.New("no write connection"))
	}
	if arg == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE `+where, arg)
	if err != nil {
		return 0, apperr.Internal("delete web sessions", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// formatTimeOrEmpty renders a timestamp like formatTime but keeps the zero
// value as "" so optional columns round-trip cleanly through parseTime.
func formatTimeOrEmpty(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTime(value)
}
