package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// unusableAddr makes net.Listen fail at once. run and runReader then wire the
// whole process, hit the listener error, stop their background loops and
// return, so the wiring is exercised without a signal or an open port.
const unusableAddr = "127.0.0.1:not-a-port"

// isolateProcess keeps a run test away from the host: no user config file, no
// self-reporting, the logger and the arguments restored afterwards.
func isolateProcess(t *testing.T, args ...string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{
		"BUGBARN_SELF_ENDPOINT", "BUGBARN_SELF_API_KEY", "BUGBARN_FUNNELBARN_API_KEY",
		"BUGBARN_REDIS_QUEUE_URL", "BUGBARN_MODE", "BUGBARN_API_KEY_SHA256",
		"BUGBARN_ADMIN_USERNAME", "BUGBARN_ADMIN_PASSWORD", "BUGBARN_ADMIN_PASSWORD_BCRYPT",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "BUGBARN_OIDC_ISSUER",
	} {
		t.Setenv(key, "")
	}
	prevArgs, prevLog := os.Args, slog.Default()
	os.Args = append([]string{"bugbarn"}, args...)
	t.Cleanup(func() {
		os.Args = prevArgs
		slog.SetDefault(prevLog)
	})
}

func TestRunVersionPrintsAndReturns(t *testing.T) {
	isolateProcess(t, "version")
	if err := run(); err != nil {
		t.Fatalf("run version: %v", err)
	}
}

func TestRunWorkerOnceSubcommandReplaysAnEmptySpool(t *testing.T) {
	isolateProcess(t, "worker-once")
	dir := t.TempDir()
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "spool"))
	t.Setenv("BUGBARN_DB_PATH", filepath.Join(dir, "bugbarn.db"))
	if err := run(); err != nil {
		t.Fatalf("run worker-once: %v", err)
	}
}

// The writer and the single-process monolith share one wiring. A listener
// failure returns its error and leaves the spool and the database on disk.
func TestRunWiresTheWriterAndReturnsTheListenerError(t *testing.T) {
	isolateProcess(t)
	dir := t.TempDir()
	t.Setenv("BUGBARN_ADDR", unusableAddr)
	t.Setenv("BUGBARN_DB_PATH", filepath.Join(dir, "bugbarn.db"))
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "spool"))
	t.Setenv("BUGBARN_SESSION_SECRET", "test-secret")
	t.Setenv("BUGBARN_API_KEY", "test-key")
	t.Setenv("BUGBARN_ENVIRONMENT", "testing")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("run = %v, want the listener error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "bugbarn.db")); statErr != nil {
		t.Errorf("database not created: %v", statErr)
	}
}

func TestEnsureProductionAPIKeyAuth(t *testing.T) {
	prod := config.Config{Environment: "production"}
	if err := ensureProductionAPIKeyAuth(prod, auth.New("")); err == nil || !strings.Contains(err.Error(), "refusing to start") {
		t.Errorf("production without a key = %v, want the guard", err)
	}
	if err := ensureProductionAPIKeyAuth(prod, auth.New("k")); err != nil {
		t.Errorf("production with a key = %v", err)
	}
	if err := ensureProductionAPIKeyAuth(config.Config{Environment: "testing"}, auth.New("")); err != nil {
		t.Errorf("testing without a key = %v", err)
	}
}

// seedDatabase creates the database file a reader opens read-only.
func seedDatabase(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "bugbarn.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatalf("seed database: %v", err)
	}
	store.Close()
	return path
}

func TestRunReaderWiresAndReturnsTheListenerError(t *testing.T) {
	isolateProcess(t, "")
	os.Args = []string{"bugbarn"}
	dir := t.TempDir()
	t.Setenv("BUGBARN_MODE", "reader")
	t.Setenv("BUGBARN_WRITER_URL", "http://127.0.0.1:1")
	t.Setenv("BUGBARN_ADDR", unusableAddr)
	t.Setenv("BUGBARN_DB_PATH", seedDatabase(t, dir))
	t.Setenv("BUGBARN_SESSION_SECRET", "test-secret")
	t.Setenv("BUGBARN_API_KEY", "test-key")
	t.Setenv("BUGBARN_SPOOL_DIR", filepath.Join(dir, "reader-spool"))
	t.Setenv("BUGBARN_ENVIRONMENT", "testing")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("run = %v, want the listener error", err)
	}
}

func TestRunReaderFailsFastOnItsPreconditions(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, dir string)
		want  string
	}{
		"missing database": {
			setup: func(t *testing.T, dir string) {
				t.Setenv("BUGBARN_DB_PATH", filepath.Join(dir, "absent", "bugbarn.db"))
			},
			want: "open read-only storage",
		},
		"login without the shared session secret": {
			setup: func(t *testing.T, dir string) {
				t.Setenv("BUGBARN_DB_PATH", seedDatabase(t, dir))
				t.Setenv("BUGBARN_ADMIN_USERNAME", "admin")
				t.Setenv("BUGBARN_ADMIN_PASSWORD", "hunter2hunter2")
				t.Setenv("BUGBARN_SESSION_SECRET", "")
			},
			want: "BUGBARN_SESSION_SECRET",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			isolateProcess(t)
			t.Setenv("BUGBARN_MODE", "reader")
			t.Setenv("BUGBARN_WRITER_URL", "http://127.0.0.1:1")
			t.Setenv("BUGBARN_ADDR", unusableAddr)
			tc.setup(t, t.TempDir())
			err := run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestWaitForBackgroundReturnsAtTheDeadline(t *testing.T) {
	logs := captureLogs(t)
	var wgStuck sync.WaitGroup
	wgStuck.Add(1)
	defer wgStuck.Done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitForBackground(ctx, &wgStuck)
	if !strings.Contains(logs.String(), "did not stop") {
		t.Errorf("deadline not logged: %s", logs.String())
	}
}

func TestWaitForBackgroundReturnsOnceDrained(t *testing.T) {
	var wg sync.WaitGroup
	waitForBackground(context.Background(), &wg)
}
