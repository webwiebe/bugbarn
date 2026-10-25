package auth

import (
	"crypto/subtle"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// login is one username with its bcrypt hash.
type login struct {
	username string
	hash     []byte
}

func (l login) enabled() bool { return l.username != "" && len(l.hash) > 0 }

// WithSmokeLogin adds the optional second login that the post-deploy browser
// smoke signs in with. It has the same session and permissions as the admin
// login. Both values empty leaves it disabled, which is the default in every
// environment. The password is bcrypt only: there is no plaintext variant.
func (a *UserAuthenticator) WithSmokeLogin(username, passwordBcrypt string) (*UserAuthenticator, error) {
	username = strings.TrimSpace(username)
	passwordBcrypt = strings.TrimSpace(passwordBcrypt)
	if username == "" && passwordBcrypt == "" {
		return a, nil
	}
	if username == "" || passwordBcrypt == "" {
		return nil, errors.New("the smoke login needs both a username and a bcrypt password hash")
	}
	if a == nil || !a.Enabled() {
		return nil, errors.New("the smoke login needs the admin login to be configured")
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) == 1 {
		return nil, errors.New("the smoke login must use a different username than the admin login")
	}
	if _, err := bcrypt.Cost([]byte(passwordBcrypt)); err != nil {
		return nil, errors.New("the smoke password hash is not a bcrypt hash")
	}
	a.smoke = login{username: username, hash: []byte(passwordBcrypt)}
	return a, nil
}

// Authenticate checks a username and password against every configured login
// and returns the username that matched. Every enabled login gets both a
// constant-time username compare and a bcrypt compare, with no early return,
// so the response time does not say which user exists. A disabled
// authenticator accepts everyone and returns an empty username.
func (a *UserAuthenticator) Authenticate(username, password string) (string, bool) {
	if a == nil || !a.Enabled() {
		return "", true
	}
	matched := ""
	found := 0
	for _, l := range []login{{a.username, a.passwordHash}, a.smoke} {
		if !l.enabled() {
			continue
		}
		userOK := subtle.ConstantTimeCompare([]byte(username), []byte(l.username))
		passOK := 0
		if bcrypt.CompareHashAndPassword(l.hash, []byte(password)) == nil {
			passOK = 1
		}
		if userOK&passOK == 1 {
			matched = l.username
			found = 1
		}
	}
	return matched, found == 1
}
