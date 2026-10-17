package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminUser = "admin"
	adminPass = "admin-pass"
	smokeUser = "e2e-bugbarn-smoke@e2e.wiebe.xyz"
	smokePass = "smoke-pass"
)

func hashOf(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

func authWithSmoke(t *testing.T) *UserAuthenticator {
	t.Helper()
	a, err := NewUserAuthenticator(adminUser, adminPass, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err = a.WithSmokeLogin(smokeUser, hashOf(t, smokePass))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSmokeLoginAdminStillWorks(t *testing.T) {
	a := authWithSmoke(t)
	user, ok := a.Authenticate(adminUser, adminPass)
	if !ok || user != adminUser {
		t.Fatalf("Authenticate(admin) = %q, %v; want %q, true", user, ok, adminUser)
	}
	if !a.Valid(adminUser, adminPass) {
		t.Fatal("Valid(admin) must keep accepting the admin pair")
	}
}

func TestSmokeLoginWorksWhenSet(t *testing.T) {
	a := authWithSmoke(t)
	user, ok := a.Authenticate(smokeUser, smokePass)
	if !ok || user != smokeUser {
		t.Fatalf("Authenticate(smoke) = %q, %v; want %q, true", user, ok, smokeUser)
	}
	if !a.Valid(smokeUser, smokePass) {
		t.Fatal("Valid(smoke) must accept the smoke pair")
	}
}

func TestSmokeLoginRejectedWhenUnset(t *testing.T) {
	a, err := NewUserAuthenticator(adminUser, adminPass, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Valid(smokeUser, smokePass) {
		t.Fatal("smoke pair must be rejected when no smoke login is configured")
	}
	a, err = a.WithSmokeLogin("", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Valid(smokeUser, smokePass) || a.Valid("", "") {
		t.Fatal("empty smoke config must leave the smoke login disabled")
	}
}

func TestSmokeLoginWrongPasswordRejectedForBoth(t *testing.T) {
	a := authWithSmoke(t)
	if a.Valid(adminUser, smokePass) {
		t.Fatal("admin with the smoke password must fail")
	}
	if a.Valid(smokeUser, adminPass) {
		t.Fatal("smoke with the admin password must fail")
	}
	if a.Valid(adminUser, "wrong") || a.Valid(smokeUser, "wrong") {
		t.Fatal("a wrong password must fail for both logins")
	}
	if user, ok := a.Authenticate(smokeUser, "wrong"); ok || user != "" {
		t.Fatalf("Authenticate(wrong) = %q, %v; want empty, false", user, ok)
	}
}

func TestSmokeLoginEmptyHashNeverMatches(t *testing.T) {
	a, err := NewUserAuthenticator(adminUser, adminPass, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WithSmokeLogin(smokeUser, ""); err == nil {
		t.Fatal("a smoke username without a hash must be a configuration error")
	}
	if a.Valid(smokeUser, "") {
		t.Fatal("an empty hash must never match an empty password")
	}
}

func TestSmokeLoginConfigErrors(t *testing.T) {
	admin, err := NewUserAuthenticator(adminUser, adminPass, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		auth     *UserAuthenticator
		username string
		hash     string
	}{
		"hash without a username":     {admin, "", hashOf(t, smokePass)},
		"plaintext in the hash field": {admin, smokeUser, smokePass},
		"same username as the admin":  {admin, adminUser, hashOf(t, smokePass)},
		"no admin login to attach to": {&UserAuthenticator{}, smokeUser, hashOf(t, smokePass)},
	}
	for name, c := range cases {
		if _, err := c.auth.WithSmokeLogin(c.username, c.hash); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestAuthenticateWhenDisabledAcceptsAnyone(t *testing.T) {
	a, err := NewUserAuthenticator("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Authenticate("x", "y"); !ok {
		t.Fatal("a disabled authenticator keeps accepting everyone, as Valid did")
	}
}
