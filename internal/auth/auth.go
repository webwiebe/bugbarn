package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const HeaderAPIKey = "x-bugbarn-api-key"

type Authorizer struct {
	apiKeyHash []byte
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

func (a *Authorizer) Enabled() bool {
	return a != nil && len(a.apiKeyHash) == sha256.Size
}

func (a *Authorizer) Valid(provided string) bool {
	if a == nil || !a.Enabled() {
		return true
	}

	sum := sha256.Sum256([]byte(strings.TrimSpace(provided)))
	if len(a.apiKeyHash) != len(sum) {
		return false
	}

	return subtle.ConstantTimeCompare(sum[:], a.apiKeyHash) == 1
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

type SessionManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

type sessionClaims struct {
	Username string `json:"u"`
	Expires  int64  `json:"e"`
	Nonce    string `json:"n"`
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
		now:    time.Now,
	}
}

func (m *SessionManager) Create(username string) (string, time.Time, error) {
	if m == nil {
		return "", time.Time{}, errors.New("session manager is nil")
	}
	expires := m.now().UTC().Add(m.ttl)
	claims := sessionClaims{
		Username: username,
		Expires:  expires.Unix(),
		Nonce:    randomSecret(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	signature := sign(m.secret, payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	return token, expires, nil
}

func (m *SessionManager) Valid(token string) (string, bool) {
	if m == nil || strings.TrimSpace(token) == "" {
		return "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	expected := sign(m.secret, payload)
	if hmac.Equal(signature, expected) {
		var claims sessionClaims
		if err := json.Unmarshal(payload, &claims); err != nil {
			return "", false
		}
		if claims.Expires <= m.now().UTC().Unix() || claims.Username == "" {
			return "", false
		}
		return claims.Username, true
	}
	return "", false
}

func SessionCookie(token string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_session",
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

func ClearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     "bugbarn_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
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
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
