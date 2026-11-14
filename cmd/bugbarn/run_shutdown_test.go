package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
)

// stopImmediately replaces the signal context with one that is already
// canceled, so a role wires everything and then takes its shutdown path
// without a process signal.
func stopImmediately(t *testing.T) {
	t.Helper()
	prev := notifyContext
	notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, cancel
	}
	t.Cleanup(func() { notifyContext = prev })
}

func TestNotifyContextDefaultsToSignalNotifyContext(t *testing.T) {
	ctx, stop := notifyContext(context.Background(), os.Interrupt)
	defer stop()
	if ctx.Err() != nil {
		t.Fatalf("context canceled before any signal: %v", ctx.Err())
	}
	signal.Reset(os.Interrupt)
}

// sink answers every request, so the self-report and FunnelBarn clients have
// somewhere to flush at shutdown.
func sink(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunWriterShutsDownCleanlyWithEveryOptionalPartWired(t *testing.T) {
	isolateProcess(t)
	stopImmediately(t)
	ext := sink(t)
	dir := t.TempDir()
	t.Setenv("BUGBARN_ADDR", "127.0.0.1:0")
	t.Setenv("BUGBARN_DB_PATH", filepath.Join(dir, "bugbarn.db"))
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "spool"))
	t.Setenv("BUGBARN_SESSION_SECRET", "test-secret")
	t.Setenv("BUGBARN_API_KEY", "test-key")
	t.Setenv("BUGBARN_ENVIRONMENT", "testing")
	t.Setenv("BUGBARN_COMPACT_ON_START", "true")
	t.Setenv("BUGBARN_SELF_ENDPOINT", ext.URL)
	t.Setenv("BUGBARN_SELF_API_KEY", "self-key")
	t.Setenv("BUGBARN_FUNNELBARN_API_KEY", "fb-key")
	t.Setenv("BUGBARN_FUNNELBARN_ENDPOINT", ext.URL)
	t.Setenv("BUGBARN_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("BUGBARN_MAX_SOURCE_MAP_BYTES", "1048576")
	t.Setenv("BUGBARN_OIDC_ISSUER", ext.URL)
	t.Setenv("BUGBARN_OIDC_CLIENT_ID", "client")
	t.Setenv("BUGBARN_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("BUGBARN_OIDC_REDIRECT_URL", "http://localhost/cb")
	t.Setenv("BUGBARN_REDIS_QUEUE_URL", "redis://127.0.0.1:1")
	t.Setenv("BUGBARN_CLOUDFLARE_API_TOKEN", "cf-token")
	t.Setenv("BUGBARN_CLOUDFLARE_ZONE_IDS", "zone1")

	if err := run(); err != nil {
		t.Fatalf("run returned %v after a clean shutdown", err)
	}
}

func TestRunReaderShutsDownCleanlyWithEveryOptionalPartWired(t *testing.T) {
	isolateProcess(t)
	stopImmediately(t)
	ext := sink(t)
	dir := t.TempDir()
	t.Setenv("BUGBARN_MODE", "reader")
	t.Setenv("BUGBARN_WRITER_URL", ext.URL)
	t.Setenv("BUGBARN_ADDR", "127.0.0.1:0")
	t.Setenv("BUGBARN_DB_PATH", seedDatabase(t, dir))
	t.Setenv("BUGBARN_SESSION_SECRET", "test-secret")
	t.Setenv("BUGBARN_API_KEY", "test-key")
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "reader-spool"))
	t.Setenv("BUGBARN_ENVIRONMENT", "testing")
	t.Setenv("BUGBARN_SELF_ENDPOINT", ext.URL)
	t.Setenv("BUGBARN_SELF_API_KEY", "self-key")
	t.Setenv("BUGBARN_FUNNELBARN_API_KEY", "fb-key")
	t.Setenv("BUGBARN_FUNNELBARN_ENDPOINT", ext.URL)
	t.Setenv("BUGBARN_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("BUGBARN_MAX_SOURCE_MAP_BYTES", "1048576")
	t.Setenv("BUGBARN_OIDC_ISSUER", ext.URL)
	t.Setenv("BUGBARN_OIDC_CLIENT_ID", "client")
	t.Setenv("BUGBARN_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("BUGBARN_OIDC_REDIRECT_URL", "http://localhost/cb")

	if err := run(); err != nil {
		t.Fatalf("run returned %v after a clean shutdown", err)
	}
}

func TestRunReaderDrainsToRedisWhenAQueueIsConfigured(t *testing.T) {
	isolateProcess(t)
	stopImmediately(t)
	dir := t.TempDir()
	t.Setenv("BUGBARN_MODE", "reader")
	t.Setenv("BUGBARN_WRITER_URL", "http://127.0.0.1:1")
	t.Setenv("BUGBARN_ADDR", "127.0.0.1:0")
	t.Setenv("BUGBARN_DB_PATH", seedDatabase(t, dir))
	t.Setenv("BUGBARN_API_KEY", "test-key")
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "reader-spool"))
	t.Setenv("BUGBARN_REDIS_QUEUE_URL", "redis://127.0.0.1:1")

	if err := run(); err != nil {
		t.Fatalf("run returned %v", err)
	}
}

func TestRunReaderRejectsABadRedisURL(t *testing.T) {
	isolateProcess(t)
	dir := t.TempDir()
	t.Setenv("BUGBARN_MODE", "reader")
	t.Setenv("BUGBARN_WRITER_URL", "http://127.0.0.1:1")
	t.Setenv("BUGBARN_ADDR", unusableAddr)
	t.Setenv("BUGBARN_DB_PATH", seedDatabase(t, dir))
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "reader-spool"))
	t.Setenv("BUGBARN_REDIS_QUEUE_URL", "://bad")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "open write queue") {
		t.Fatalf("run = %v, want the write queue error", err)
	}
}
