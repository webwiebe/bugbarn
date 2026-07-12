package auth

import (
	"context"
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
	if manager.TTL() != time.Hour {
		t.Fatalf("TTL() = %v, want 1h", manager.TTL())
	}
	if def := NewSessionManager("s", 0).TTL(); def != 12*time.Hour {
		t.Fatalf("default TTL = %v, want 12h", def)
	}

	// CSRF tokens are deterministic per (secret, handle) and differ across
	// secrets/handles.
	handle := NewSessionHandle()
	first := manager.CSRFToken(handle)
	if again := manager.CSRFToken(handle); first != again {
		t.Fatalf("CSRF token must be deterministic: %q vs %q", first, again)
	}
	if manager.CSRFToken(handle) == manager.CSRFToken(NewSessionHandle()) {
		t.Fatal("CSRF token must differ per handle")
	}
	if NewSessionManager("other-secret", time.Hour).CSRFToken(handle) == manager.CSRFToken(handle) {
		t.Fatal("CSRF token must differ per secret")
	}
}

func TestSessionHandles(t *testing.T) {
	h1, h2 := NewSessionHandle(), NewSessionHandle()
	if h1 == "" || h1 == h2 {
		t.Fatalf("handles must be non-empty and unique, got %q / %q", h1, h2)
	}
	hash := HashSessionHandle(h1)
	if len(hash) != 64 || hash == HashSessionHandle(h2) {
		t.Fatalf("hash must be sha256 hex and unique per handle, got %q", hash)
	}
	if hash != HashSessionHandle(h1) {
		t.Fatal("hash must be deterministic")
	}
}

func TestValidWithProject_StaticKey(t *testing.T) {
	a := New("secret")

	pid, scope, ok := a.ValidWithProject(context.Background(), "secret")
	if !ok {
		t.Fatal("expected static key to be accepted")
	}
	if pid != 0 || scope != "full" {
		t.Fatalf("expected pid=0 scope=full, got pid=%d scope=%q", pid, scope)
	}

	_, _, ok = a.ValidWithProject(context.Background(), "wrong")
	if ok {
		t.Fatal("expected wrong key to be rejected")
	}
}

func TestValidWithProject_DBLookup(t *testing.T) {
	var touched string
	lookup := func(_ context.Context, sha string) (int64, string, bool, error) {
		if sha == "knownhash" {
			return 42, "ingest", true, nil
		}
		return 0, "", false, nil
	}
	touch := func(_ context.Context, sha string) error {
		touched = sha
		return nil
	}

	a := New("").WithDBLookup(lookup, touch)

	sum := sha256.Sum256([]byte("mykey"))
	hexSum := hex.EncodeToString(sum[:])

	// Key not in DB.
	_, _, ok := a.ValidWithProject(context.Background(), "mykey")
	if ok {
		t.Fatal("expected unknown DB key to be rejected")
	}

	// Inject a lookup that matches the key hash.
	a = New("").WithDBLookup(func(_ context.Context, sha string) (int64, string, bool, error) {
		if sha == hexSum {
			return 7, "ingest", true, nil
		}
		return 0, "", false, nil
	}, touch)

	pid, scope, ok := a.ValidWithProject(context.Background(), "mykey")
	if !ok {
		t.Fatal("expected DB key to be accepted")
	}
	if pid != 7 || scope != "ingest" {
		t.Fatalf("expected pid=7 scope=ingest, got pid=%d scope=%q", pid, scope)
	}
	if touched != hexSum {
		t.Fatalf("expected touch to be called with %q, got %q", hexSum, touched)
	}
}

func TestValidWithSetupFallback(t *testing.T) {
	sum := sha256.Sum256([]byte("realkey"))
	hexSum := hex.EncodeToString(sum[:])

	lookup := func(_ context.Context, sha string) (int64, string, bool, error) {
		if sha == hexSum {
			return 1, "ingest", true, nil
		}
		return 0, "", false, nil
	}

	var setupCalled bool
	setup := func(_ context.Context, rawKey, slug string) (int64, bool) {
		setupCalled = true
		if rawKey == "setupkey" && slug == "my-project" {
			return 99, true
		}
		return 0, false
	}

	a := New("").WithDBLookup(lookup, nil).WithSetupKeyVerifier(setup)

	// Normal DB key hits — setup verifier must not be called.
	pid, scope, ok := a.ValidWithSetupFallback(context.Background(), "realkey", "my-project")
	if !ok || pid != 1 || scope != "ingest" {
		t.Fatalf("expected DB key match, got ok=%v pid=%d scope=%q", ok, pid, scope)
	}
	if setupCalled {
		t.Fatal("setup verifier must not be called when normal auth succeeds")
	}

	// Unknown key falls through to setup verifier.
	pid, scope, ok = a.ValidWithSetupFallback(context.Background(), "setupkey", "my-project")
	if !ok || pid != 99 || scope != "ingest" {
		t.Fatalf("expected setup key match, got ok=%v pid=%d scope=%q", ok, pid, scope)
	}

	// Unknown key, setup verifier also rejects.
	_, _, ok = a.ValidWithSetupFallback(context.Background(), "unknown", "my-project")
	if ok {
		t.Fatal("expected rejection when both DB and setup verifier fail")
	}
}
