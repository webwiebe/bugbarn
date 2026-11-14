package telemetrydb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunMetricsJobsRollsUpAndExpiresOnTick(t *testing.T) {
	d := openTest(t, Metrics, 0)
	seedHours(t, d, 2)
	// The 2026 samples are far behind the real clock, so the first pass rolls
	// both hours up and retention then drops the minute samples.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.RunMetricsJobs(ctx, time.Millisecond, Retention{Raw: time.Hour, Hourly: 0}, quiet())
		close(done)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for {
		var rolled, raw int
		if err := d.Read().QueryRow(`SELECT COUNT(*) FROM samples_1h`).Scan(&rolled); err != nil {
			t.Fatal(err)
		}
		if err := d.Read().QueryRow(`SELECT COUNT(*) FROM samples_1m`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if rolled > 0 && raw == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 30s: %d hourly rows, %d minute rows", rolled, raw)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunMetricsJobs did not return after cancel")
	}
}

func TestRunMetricsJobsReturnsAtOnceOnAReadOnlyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	w, err := Open(context.Background(), Metrics, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, err := OpenReadOnly(context.Background(), Metrics, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	done := make(chan struct{})
	go func() { r.RunMetricsJobs(context.Background(), 0, Retention{}, quiet()); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunMetricsJobs on a read-only DB kept running")
	}
	if got := r.Path(); got != path {
		t.Fatalf("Path = %q, want %q", got, path)
	}
	if n, err := r.Rollup(context.Background(), t0); err != nil || n != 0 {
		t.Fatalf("read-only Rollup = %d, %v", n, err)
	}
	if err := r.ApplyRetention(context.Background(), Retention{Raw: time.Hour}, t0); err != nil {
		t.Fatalf("read-only ApplyRetention: %v", err)
	}
}

func TestRunMetricsJobsDefaultsAZeroIntervalAndStopsOnCancel(t *testing.T) {
	d := openTest(t, Metrics, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.RunMetricsJobs(ctx, 0, Retention{}, quiet()); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunMetricsJobs did not return after cancel")
	}
}

func TestOpenFailsWhenTheDirectoryCannotBeCreated(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Metrics, filepath.Join(blocker, "sub", "metrics.db"), 0); err == nil {
		t.Fatal("Open under a regular file succeeded")
	}
}

func TestOpenFailsOnAnUnknownMigrationSet(t *testing.T) {
	t.Parallel()
	_, err := Open(context.Background(), Spec{Name: "nonexistent"}, filepath.Join(t.TempDir(), "x.db"), 0)
	if err == nil {
		t.Fatal("Open with a spec that has no migrations succeeded")
	}
}

// A file the writer has created but not yet migrated reads as not created.
func TestOpenReadOnlyTreatsAnEmptySchemaAsNotCreated(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "metrics.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE unrelated (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	_, err = OpenReadOnly(context.Background(), Metrics, path)
	if !errors.Is(err, ErrNotCreated) {
		t.Fatalf("OpenReadOnly = %v, want ErrNotCreated", err)
	}
}
