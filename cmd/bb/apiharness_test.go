package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// The command tests run against the real API (internal/api with a temporary
// SQLite store) behind an httptest server. The CLI and the server share their
// JSON contract only through these requests, so a fake server would let the two
// drift apart unnoticed. Error mapping (401, 5xx, network failure) uses small
// scripted servers instead, because the real API cannot be told to fail on cue.

const (
	harnessUser     = "admin"
	harnessPassword = "correct horse"
)

// harness is a running API and the seeded data behind it.
type harness struct {
	URL    string
	APIKey string
	Store  *storage.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	users, err := auth.NewUserAuthenticator(harnessUser, harnessPassword, "")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	handler := api.NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, users, auth.NewSessionManager("test-secret", time.Hour), nil, logger)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	project, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatalf("default project: %v", err)
	}
	const plaintext = "harness-api-key-0123456789"
	sum := sha256.Sum256([]byte(plaintext))
	if _, err := store.CreateAPIKey(ctx, "harness", project.ID, hex.EncodeToString(sum[:]), storage.APIKeyScopeFull); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	h := &harness{URL: srv.URL, APIKey: plaintext, Store: store}
	useConfig(t, Config{URL: srv.URL, Auth: AuthConfig{Type: "apikey", APIKey: plaintext}})
	return h
}

// useConfig writes cfg to a temp file and makes it the CLI's config through
// BB_CONFIG. Telemetry stays off so a failing command never reports to a
// BugBarn.
func useConfig(t *testing.T, cfg Config) string {
	t.Helper()
	off := false
	cfg.Telemetry = &off
	path := filepath.Join(t.TempDir(), "cli.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_CONFIG", path)
	return path
}

// inEmptyDir moves the process into a directory with no .bugbarn.json above it,
// so findLocalConfig cannot pick up a project from the developer's checkout.
func inEmptyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

// seedIssue stores one event, which creates its issue, and returns the issue.
func (h *harness) seedIssue(t *testing.T, message string) storage.Issue {
	t.Helper()
	now := time.Now().UTC()
	pe := event.ProcessedEvent{
		Event: event.Event{
			ObservedAt: now,
			ReceivedAt: now.Add(time.Second),
			Severity:   "ERROR",
			Message:    message,
			Exception:  event.Exception{Type: "TestError", Message: message},
		},
		Fingerprint:         "fp-" + message,
		FingerprintMaterial: "TestError: " + message,
	}
	issue, _, _, _, err := h.Store.PersistProcessedEvent(context.Background(), pe)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	return issue
}

// writeFile writes content to dir/name.
func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
}

// runCmd calls a command function and returns its combined output and error.
func runCmd(t *testing.T, cmd func([]string) error, args ...string) (string, error) {
	t.Helper()
	var err error
	out := quiet(t, func() { err = cmd(args) })
	return out, err
}
