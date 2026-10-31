package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	setup := func(_ context.Context, rawKey, slug string) (int64, bool, error) {
		setupCalled = true
		if rawKey == "setupkey" && slug == "my-project" {
			return 99, true, nil
		}
		return 0, false, nil
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

// TestProjectForKeyDoesNotTouch: ProjectForKey resolves a key's project without
// updating last_used_at. Callers use it after the key was already validated on
// the same request; on the read-only reader store the touch is a guaranteed
// failed write, so a second one per ingest is pure noise.
func TestProjectForKeyDoesNotTouch(t *testing.T) {
	const key = "db-key"
	sum := sha256.Sum256([]byte(key))
	hexSum := hex.EncodeToString(sum[:])

	touched := 0
	a := (&Authorizer{}).WithDBLookup(
		func(_ context.Context, provided string) (int64, string, bool, error) {
			if provided != hexSum {
				return 0, "", false, nil
			}
			return 7, "ingest", true, nil
		},
		func(context.Context, string) error { touched++; return nil },
	)

	pid, scope, ok := a.ProjectForKey(context.Background(), key)
	if !ok || pid != 7 || scope != "ingest" {
		t.Fatalf("ProjectForKey = (%d, %q, %v), want (7, ingest, true)", pid, scope, ok)
	}
	if touched != 0 {
		t.Errorf("touch called %d times, want 0", touched)
	}

	// ValidWithProject keeps touching — that path is the one that records use.
	if _, _, ok := a.ValidWithProject(context.Background(), key); !ok {
		t.Fatal("ValidWithProject rejected a valid key")
	}
	if touched != 1 {
		t.Errorf("touch called %d times after ValidWithProject, want 1", touched)
	}
}

// An empty key can match nothing, so it is rejected without a store query: a
// request with no key gets 401 during a store outage, not a retryable 503.
func TestEmptyKeyIsRejectedWithoutALookup(t *testing.T) {
	lookups := 0
	a := New("").WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
		lookups++
		return 0, "", false, errors.New("database is locked")
	}, nil)
	for _, key := range []string{"", "   "} {
		_, _, ok, err := a.resolveKeyErr(context.Background(), key, false)
		if ok || err != nil {
			t.Errorf("key %q: ok=%v err=%v, want a plain rejection", key, ok, err)
		}
	}
	if lookups != 0 {
		t.Errorf("store queried %d times for an empty key", lookups)
	}
}

// A store error during the key lookup is not a verdict on the key: it comes back
// as ErrKeyLookup, the setup fallback is not tried, and a key that is simply
// missing stays a plain rejection with no error.
func TestCheckWithSetupFallbackSeparatesLookupErrorFromMissingKey(t *testing.T) {
	setupCalled := false
	verifier := func(context.Context, string, string) (int64, bool, error) {
		setupCalled = true
		return 9, true, nil
	}

	failing := New("").
		WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
			return 0, "", false, errors.New("database is locked")
		}, nil).
		WithSetupKeyVerifier(verifier)
	_, _, ok, err := failing.CheckWithSetupFallback(context.Background(), "k", "proj")
	if ok || !errors.Is(err, ErrKeyLookup) {
		t.Fatalf("lookup error: ok=%v err=%v, want ok=false and ErrKeyLookup", ok, err)
	}
	if setupCalled {
		t.Fatal("setup fallback ran after a failed lookup")
	}
	if _, _, valid := failing.ValidWithSetupFallback(context.Background(), "k", "proj"); valid {
		t.Fatal("ValidWithSetupFallback accepted a key whose lookup failed")
	}

	missing := New("").WithDBLookup(func(context.Context, string) (int64, string, bool, error) {
		return 0, "", false, nil
	}, nil)
	_, _, ok, err = missing.CheckWithSetupFallback(context.Background(), "k", "")
	if ok || err != nil {
		t.Fatalf("missing key: ok=%v err=%v, want ok=false and nil error", ok, err)
	}
}
