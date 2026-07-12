// Package sessionstore manages the server-side web_sessions rows behind the
// opaque browser cookie. It exists as an abstraction because of the CQRS
// deployment: the single-process and writer modes talk to SQLite directly
// (Direct), while the read-only reader replicas validate sessions against
// their local SQLite mount but delegate every mutation — create, refresh,
// delete — to writer-internal HTTP endpoints (Remote). Refresh executes only
// on the writer so the single-use iambarn refresh tokens are never raced
// across replicas.
package sessionstore

import (
	"context"
	"errors"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// ErrNotFound is returned when no session row exists for the handle hash.
var ErrNotFound = errors.New("sessionstore: session not found")

// ErrRevoked is returned when the session is gone for good: the IdP rejected
// the refresh token (invalid_grant) or the row was deleted. Callers must
// treat the cookie as dead and force a re-login.
var ErrRevoked = errors.New("sessionstore: session revoked")

// ErrTransient wraps refresh failures that may heal (IdP outage, network,
// writer unreachable). The middleware serves the stale session within the
// bounded grace window and returns 401 past it.
var ErrTransient = errors.New("sessionstore: transient refresh failure")

// Store is the session persistence + lifecycle interface shared by the
// single-process/writer (Direct) and reader (Remote) implementations.
type Store interface {
	// Create persists a new session row.
	Create(ctx context.Context, ws storage.WebSession) error
	// Get loads a session row by handle hash without side effects.
	Get(ctx context.Context, idHash string) (storage.WebSession, error)
	// Refresh renews the session's IdP tokens when they are (nearly) expired
	// and returns the current row. On invalid_grant the row is deleted and
	// ErrRevoked returned. On transient failure it returns the stale row
	// (refresh_failing_since set) together with an error wrapping ErrTransient.
	Refresh(ctx context.Context, idHash string) (storage.WebSession, error)
	// Delete removes a session row.
	Delete(ctx context.Context, idHash string) error
	// DeleteBySID removes all sessions bound to an IdP session id.
	DeleteBySID(ctx context.Context, sid string) (int64, error)
	// DeleteBySub removes all sessions for an IdP subject.
	DeleteBySub(ctx context.Context, sub string) (int64, error)
}

// accessSkew is how early a session's access token is considered expired, so
// a token that dies mid-request is refreshed just before instead.
const accessSkew = 30 // seconds
