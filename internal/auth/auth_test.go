package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestValidWhenDisabled(t *testing.T) {
	a := New("")

	if !a.Valid("") {
		t.Fatal("expected disabled auth to accept empty key")
	}

	if !a.Valid("anything") {
		t.Fatal("expected disabled auth to accept any key")
	}
}

func TestValidWhenEnabled(t *testing.T) {
	a := New("secret")

	if a.Valid("") {
		t.Fatal("expected missing key to be rejected")
	}

	if a.Valid("wrong") {
		t.Fatal("expected wrong key to be rejected")
	}

	if !a.Valid("secret") {
		t.Fatal("expected exact key to be accepted")
	}
}

func TestValidWithHashedAPIKey(t *testing.T) {
	sum := sha256.Sum256([]byte("secret"))
	a, err := NewHashed(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}

	if a.Valid("wrong") {
		t.Fatal("expected wrong key to be rejected")
	}

	if !a.Valid("secret") {
		t.Fatal("expected matching key to be accepted")
	}
}

func TestUserAuthenticator(t *testing.T) {
	a, err := NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}

	if !a.Enabled() {
		t.Fatal("expected authenticator to be enabled")
	}
	if !a.Valid("admin", "change-me") {
		t.Fatal("expected matching credentials to pass")
	}
	if a.Valid("admin", "wrong") {
		t.Fatal("expected wrong password to fail")
	}
	if a.Valid("other", "change-me") {
		t.Fatal("expected wrong username to fail")
	}
}

func TestSessionManager(t *testing.T) {
	manager := NewSessionManager("test-secret", time.Hour)
	now := time.Date(2026, 4, 16, 8, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	token, _, err := manager.Create("admin")
	if err != nil {
		t.Fatal(err)
	}

	if username, ok := manager.Valid(token); !ok || username != "admin" {
		t.Fatalf("expected valid session for admin, got username=%q ok=%v", username, ok)
	}

	manager.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok := manager.Valid(token); ok {
		t.Fatal("expected expired session to fail")
	}
}
