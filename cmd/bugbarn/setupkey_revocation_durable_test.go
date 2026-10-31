package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// openRevocationFile opens a writer store on a file with three projects: alpha and
// gamma have a setup key row, beta only the fallback.
func openRevocationFile(t *testing.T) (*storage.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		if _, err := store.CreateProject(context.Background(), slug, slug); err != nil {
			t.Fatal(err)
		}
	}
	giveSetupRow(t, store, "alpha")
	giveSetupRow(t, store, "gamma")
	return store, path
}

// keyCheck answers the way the ingest routes do on a process wired to store.
func keyCheck(t *testing.T, store *storage.Store) func(slug string) bool {
	t.Helper()
	authorizer, err := newAPIAuthorizer(config.Config{SessionSecret: revocationSecret}, store, store.TouchAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	handler := ingest.NewHandler(authorizer, nil, 1<<20)
	return func(slug string) bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events", http.NoBody)
		req.Header.Set(auth.HeaderAPIKey, setupKey(revocationSecret, slug))
		req.Header.Set("X-BugBarn-Project", slug)
		_, _, ok, err := handler.CheckAPIKey(req)
		if err != nil {
			t.Fatalf("CheckAPIKey(%s): %v", slug, err)
		}
		return ok
	}
}

func revokeBoth(t *testing.T, store *storage.Store) {
	t.Helper()
	for _, slug := range []string{"alpha", "beta"} {
		if err := store.RevokeSetupKey(context.Background(), slug, sha256Hex(setupKey(revocationSecret, slug))); err != nil {
			t.Fatal(err)
		}
	}
}

func expectRevocationOutcome(t *testing.T, stage string, accepted func(string) bool) {
	t.Helper()
	for _, slug := range []string{"alpha", "beta"} {
		if accepted(slug) {
			t.Fatalf("%s: the revoked key of %s was accepted", stage, slug)
		}
	}
	if !accepted("gamma") {
		t.Fatalf("%s: a key that was not revoked must keep working", stage)
	}
}

// The same process that just accepted a key refuses it on the next request, and so
// does a reader on the shared file: nothing holds an earlier answer.
func TestRevokedKeyIsRefusedOnTheNextRequest(t *testing.T) {
	store, path := openRevocationFile(t)
	reader, err := storage.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	onWriter, onReader := keyCheck(t, store), keyCheck(t, reader)

	for i := 0; i < 3; i++ {
		for _, slug := range []string{"alpha", "beta", "gamma"} {
			if !onWriter(slug) || !onReader(slug) {
				t.Fatalf("before revoking, %s must be accepted on both roles", slug)
			}
		}
	}

	revokeBoth(t, store)
	expectRevocationOutcome(t, "writer", onWriter)
	expectRevocationOutcome(t, "reader", onReader)
}

func TestRevokedKeyStaysRefusedAfterARestart(t *testing.T) {
	store, path := openRevocationFile(t)
	revokeBoth(t, store)
	store.Close()

	restarted, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	expectRevocationOutcome(t, "after a restart", keyCheck(t, restarted))

	// The setup page's insert after the restart does not bring the row back.
	alpha, err := restarted.ProjectBySlug(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.EnsureSetupAPIKey(context.Background(), "alpha-setup", alpha.ID, sha256Hex(setupKey(revocationSecret, "alpha"))); err != nil {
		t.Fatal(err)
	}
	expectRevocationOutcome(t, "after the setup page", keyCheck(t, restarted))
}

func TestRestoredSnapshotKeepsTheKeyRevoked(t *testing.T) {
	store, path := openRevocationFile(t)
	revokeBoth(t, store)

	dest := filepath.Join(t.TempDir(), "settings.db")
	if _, err := storage.SnapshotSettings(context.Background(), path, dest); err != nil {
		t.Fatal(err)
	}
	restored, err := storage.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	expectRevocationOutcome(t, "after a restore", keyCheck(t, restored))
}
