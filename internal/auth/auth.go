package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const HeaderAPIKey = "x-bugbarn-api-key"

// DBKeyLookup is called by Authorizer.ValidWithDB to check whether a SHA-256
// hex digest exists in the database. Returning (false, nil) means "not found".
// Deprecated: use DBKeyLookupWithProject instead.
type DBKeyLookup func(ctx context.Context, keySHA256 string) (bool, error)

// DBKeyLookupWithProject is called by Authorizer.ValidWithProject to look up an
// API key's project association and scope. Returns (projectID, scope, true, nil)
// on match, (0, "", false, nil) when not found.
type DBKeyLookupWithProject func(ctx context.Context, keySHA256 string) (projectID int64, scope string, found bool, err error)

// DBKeyTouch is called after a successful DB-based auth to update last_used_at.
type DBKeyTouch func(ctx context.Context, keySHA256 string) error

// SetupKeyVerifier is called when normal auth fails. It checks whether the
// provided raw key is a valid deterministic setup key and, if so, lazily
// provisions the project and API key. Returns (projectID, true) on success.
type SetupKeyVerifier func(ctx context.Context, rawKey, projectSlug string) (projectID int64, ok bool)

type Authorizer struct {
	apiKeyHash    []byte
	dbLookup      DBKeyLookupWithProject
	dbTouch       DBKeyTouch
	setupVerifier SetupKeyVerifier
}

func New(apiKey string) *Authorizer {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return &Authorizer{}
	}
	sum := sha256.Sum256([]byte(apiKey))
	return &Authorizer{apiKeyHash: sum[:]}
}

func NewHashed(apiKeySHA256 string) (*Authorizer, error) {
	apiKeySHA256 = strings.TrimSpace(apiKeySHA256)
	if apiKeySHA256 == "" {
		return &Authorizer{}, nil
	}
	decoded, err := hex.DecodeString(apiKeySHA256)
	if err != nil {
		return nil, err
	}
	if len(decoded) != sha256.Size {
		return nil, errors.New("api key hash must be a sha256 hex digest")
	}
	return &Authorizer{apiKeyHash: decoded}, nil
}

// WithDBLookup returns a copy of the Authorizer that will also accept API keys
// found in the database. The lookup and touch functions are called per-request.
func (a *Authorizer) WithDBLookup(lookup DBKeyLookupWithProject, touch DBKeyTouch) *Authorizer {
	if a == nil {
		return &Authorizer{dbLookup: lookup, dbTouch: touch}
	}
	return &Authorizer{apiKeyHash: a.apiKeyHash, dbLookup: lookup, dbTouch: touch, setupVerifier: a.setupVerifier}
}

// WithSetupKeyVerifier returns a copy of the Authorizer that will try setup key
// verification as a last resort when normal auth fails.
func (a *Authorizer) WithSetupKeyVerifier(v SetupKeyVerifier) *Authorizer {
	if a == nil {
		return &Authorizer{setupVerifier: v}
	}
	return &Authorizer{apiKeyHash: a.apiKeyHash, dbLookup: a.dbLookup, dbTouch: a.dbTouch, setupVerifier: v}
}

// Enabled returns true when at least one auth mechanism is configured.
func (a *Authorizer) Enabled() bool {
	if a == nil {
		return false
	}
	return len(a.apiKeyHash) == sha256.Size || a.dbLookup != nil
}

func (a *Authorizer) Valid(provided string) bool {
	return a.ValidWithContext(context.Background(), provided)
}

// ValidWithProject checks the provided key and returns the associated project ID and scope.
// For env-var static keys, projectID=0 and scope="full" are returned (global/admin access).
// Returns (projectID, scope, true) on success, (0, "", false) on failure.
func (a *Authorizer) ValidWithProject(ctx context.Context, provided string) (projectID int64, scope string, ok bool) {
	if a == nil || !a.Enabled() {
		return 0, "full", true
	}

	provided = strings.TrimSpace(provided)
	sum := sha256.Sum256([]byte(provided))
	hexSum := hex.EncodeToString(sum[:])

	// Check static env-var hash first (global access, no project binding).
	if len(a.apiKeyHash) == sha256.Size {
		if subtle.ConstantTimeCompare(sum[:], a.apiKeyHash) == 1 {
			return 0, "full", true
		}
	}

	// Check DB-stored keys.
	if a.dbLookup != nil {
		pid, sc, found, err := a.dbLookup(ctx, hexSum)
		if err == nil && found {
			if a.dbTouch != nil {
				if touchErr := a.dbTouch(ctx, hexSum); touchErr != nil {
					slog.WarnContext(ctx, "auth: failed to update api key last-used timestamp", "project_id", pid, "error", touchErr)
				}
			}
			return pid, sc, true
		}
	}

	return 0, "", false
}

// ValidWithSetupFallback is like ValidWithProject but also tries setup key
// verification using the project slug when normal auth fails.
func (a *Authorizer) ValidWithSetupFallback(ctx context.Context, provided, projectSlug string) (projectID int64, scope string, ok bool) {
	pid, sc, valid := a.ValidWithProject(ctx, provided)
	if valid {
		return pid, sc, true
	}
	if a.setupVerifier != nil && projectSlug != "" {
		if setupPID, setupOK := a.setupVerifier(ctx, strings.TrimSpace(provided), projectSlug); setupOK {
			return setupPID, "ingest", true
		}
	}
	return 0, "", false
}

// ValidWithContext checks the provided key against the env-var hash first, then
// the DB lookup if configured. It also calls the touch function on DB hits.
func (a *Authorizer) ValidWithContext(ctx context.Context, provided string) bool {
	_, _, ok := a.ValidWithProject(ctx, provided)
	return ok
}

type UserAuthenticator struct {
	username     string
	passwordHash []byte
}

func NewUserAuthenticator(username, password, passwordBcrypt string) (*UserAuthenticator, error) {
	username = strings.TrimSpace(username)
	passwordBcrypt = strings.TrimSpace(passwordBcrypt)
	if username == "" || (password == "" && passwordBcrypt == "") {
		return &UserAuthenticator{}, nil
	}
	if passwordBcrypt != "" {
		return &UserAuthenticator{username: username, passwordHash: []byte(passwordBcrypt)}, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &UserAuthenticator{username: username, passwordHash: hash}, nil
}

func (a *UserAuthenticator) Enabled() bool {
	return a != nil && a.username != "" && len(a.passwordHash) > 0
}

func (a *UserAuthenticator) Valid(username, password string) bool {
	if a == nil || !a.Enabled() {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) != 1 {
		return false
	}
	return bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
}

func (a *UserAuthenticator) Username() string {
	if a == nil {
		return ""
	}
	return a.username
}

// SessionManager holds the shared session secret and TTL. Sessions themselves
// are server-side rows (see internal/sessionstore) keyed by an opaque cookie
// handle; this type keys the derived CSRF tokens and carries the absolute
// session lifetime.
type SessionManager struct {
	secret []byte
	ttl    time.Duration
}

func NewSessionManager(secret string, ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		secret = randomSecret()
	}
	return &SessionManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// TTL returns the absolute session lifetime (the hard cap; day-to-day
// session validity is bound to the much shorter IdP access token).
func (m *SessionManager) TTL() time.Duration {
	if m == nil {
		return 12 * time.Hour
	}
	return m.ttl
}

// NewSessionHandle returns a fresh opaque session handle for the browser
// cookie. It carries no claims — the server-side web_sessions row (keyed by
// the handle's SHA-256) is the source of truth.
func NewSessionHandle() string {
	return randomSecret()
}

// HashSessionHandle derives the web_sessions primary key from a cookie value,
// so a database leak never exposes usable session handles.
func HashSessionHandle(handle string) string {
	sum := sha256.Sum256([]byte(handle))
	return hex.EncodeToString(sum[:])
}

func SessionCookie(token string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_session",
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		// Secure: true  // enable this behind TLS; leave off for localhost dev
	}
}

func ClearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
}

// CSRFToken derives a CSRF token bound to the session secret using HMAC-SHA256.
// The result is the first 16 bytes of the HMAC, hex-encoded (32 chars).
func (m *SessionManager) CSRFToken(sessionToken string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(sessionToken))
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

// CSRFCookie returns the companion CSRF cookie for a session token.
// HttpOnly=false so JavaScript can read and attach it as X-BugBarn-CSRF.
// SameSite=Strict matches the session cookie: this is a same-origin double-submit
// token that is never needed on a cross-site navigation.
func (m *SessionManager) CSRFCookie(sessionToken string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_csrf",
		Value:    m.CSRFToken(sessionToken),
		Path:     "/",
		Expires:  expires,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
}

// ClearCSRFCookie clears the CSRF cookie on logout.
func ClearCSRFCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_csrf",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
}

func sign(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func randomSecret() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failing is catastrophic and unrecoverable. Fail closed
		// rather than fall back to a predictable value (a timestamp) that would
		// let an attacker forge session tokens / nonces.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
