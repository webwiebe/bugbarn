package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func TestOpenReadOnlyReadsExistingData(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")

	// Create a store with read-write access and insert a project.
	rw, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := rw.CreateProject(ctx, "Read Only Test", "ro-test")
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()

	// Re-open in read-only mode.
	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Reads must work.
	got, err := ro.ProjectBySlug(ctx, "ro-test")
	if err != nil {
		t.Fatalf("ProjectBySlug on read-only store: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected project ID %d, got %d", created.ID, got.ID)
	}
	if got.Name != "Read Only Test" {
		t.Fatalf("expected project name %q, got %q", "Read Only Test", got.Name)
	}

	// Write connection must be nil.
	if ro.db != nil {
		t.Fatal("expected db (write connection) to be nil on read-only store")
	}
}

func TestOpenReadOnly_PersistProcessedEventFails(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")

	rw, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Writes must fail cleanly.
	_, _, _, _, err = ro.PersistProcessedEvent(context.Background(), dummyProcessedEvent("write-fail"))
	if err == nil {
		t.Fatal("expected error when writing to read-only store")
	}
}

func TestSwapReadDB(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPathV1 := filepath.Join(dir, "v1.db")
	dbPathV2 := filepath.Join(dir, "v2.db")

	// Create v1 database with project "alpha".
	rwV1, err := Open(dbPathV1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := rwV1.CreateProject(ctx, "Alpha", "alpha"); err != nil {
		t.Fatal(err)
	}
	rwV1.Close()

	// Create v2 database with project "beta".
	rwV2, err := Open(dbPathV2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rwV2.CreateProject(ctx, "Beta", "beta"); err != nil {
		t.Fatal(err)
	}
	rwV2.Close()

	// Open read-only against v1.
	ro, err := OpenReadOnly(dbPathV1)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Verify we see alpha but not beta.
	if _, err := ro.ProjectBySlug(ctx, "alpha"); err != nil {
		t.Fatalf("expected alpha in v1: %v", err)
	}
	if _, err := ro.ProjectBySlug(ctx, "beta"); err == nil {
		t.Fatal("did not expect beta in v1")
	}

	// Swap to v2.
	if err := ro.SwapReadDB(dbPathV2); err != nil {
		t.Fatalf("SwapReadDB: %v", err)
	}

	// Now we should see beta but not alpha (different database entirely).
	if _, err := ro.ProjectBySlug(ctx, "beta"); err != nil {
		t.Fatalf("expected beta after swap: %v", err)
	}
	if _, err := ro.ProjectBySlug(ctx, "alpha"); err == nil {
		t.Fatal("did not expect alpha after swap to v2")
	}
}

func TestSwapReadDB_InvalidPathFails(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")

	rw, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Swap to a non-existent path should fail.
	if err := ro.SwapReadDB(filepath.Join(t.TempDir(), "does-not-exist.db")); err == nil {
		t.Fatal("expected error swapping to non-existent path")
	}

	// Original connection should still work.
	if _, err := ro.ProjectBySlug(context.Background(), "default"); err != nil {
		t.Fatalf("original connection broken after failed swap: %v", err)
	}
}

func dummyProcessedEvent(fp string) worker.ProcessedEvent {
	now := time.Now()
	return worker.ProcessedEvent{
		Fingerprint: fp,
		Event: event.Event{
			ReceivedAt: now,
			ObservedAt: now,
			Severity:   "ERROR",
			Message:    "test error",
			Exception: event.Exception{
				Type:    "Error",
				Message: "test error",
			},
		},
	}
}
