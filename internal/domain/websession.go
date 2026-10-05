package domain

import "time"

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
