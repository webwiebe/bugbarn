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
