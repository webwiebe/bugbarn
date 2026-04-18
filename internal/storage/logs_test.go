package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestInsertLogEntriesAndListLogEntries(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()

	projectID := store.defaultProjectID
	now := time.Now().UTC().Truncate(time.Second)

	entries := []LogEntry{
		{ProjectID: projectID, ReceivedAt: now.Add(-3 * time.Minute), LevelNum: 30, Level: "info", Message: "hello info"},
		{ProjectID: projectID, ReceivedAt: now.Add(-2 * time.Minute), LevelNum: 40, Level: "warn", Message: "hello warn"},
		{ProjectID: projectID, ReceivedAt: now.Add(-1 * time.Minute), LevelNum: 50, Level: "error", Message: "hello error", Data: map[string]any{"code": float64(500)}},
	}

	if err := store.InsertLogEntries(ctx, entries); err != nil {
		t.Fatalf("InsertLogEntries: %v", err)
	}

	// List all — should return newest first.
	all, err := store.ListLogEntries(ctx, projectID, 0, "", 100, 0)
	if err != nil {
		t.Fatalf("ListLogEntries: %v", err)
	}
	if got, want := len(all), 3; got != want {
		t.Fatalf("expected %d entries, got %d", want, got)
	}
	if all[0].Message != "hello error" {
		t.Errorf("expected newest first, got %q", all[0].Message)
	}

	// Level filter: warn and above (levelMin=40).
	filtered, err := store.ListLogEntries(ctx, projectID, 40, "", 100, 0)
	if err != nil {
		t.Fatalf("ListLogEntries level filter: %v", err)
	}
	if got, want := len(filtered), 2; got != want {
		t.Fatalf("level filter: expected %d entries, got %d", want, got)
	}

	// q filter: substring on message.
	qFiltered, err := store.ListLogEntries(ctx, projectID, 0, "warn", 100, 0)
	if err != nil {
		t.Fatalf("ListLogEntries q filter: %v", err)
	}
	if got, want := len(qFiltered), 1; got != want {
		t.Fatalf("q filter: expected %d entries, got %d", want, got)
	}
	if qFiltered[0].Message != "hello warn" {
		t.Errorf("q filter unexpected message: %q", qFiltered[0].Message)
	}

	// Cursor: beforeID.
	beforeID := all[0].ID // newest
	cursor, err := store.ListLogEntries(ctx, projectID, 0, "", 100, beforeID)
	if err != nil {
		t.Fatalf("ListLogEntries cursor: %v", err)
	}
	if got, want := len(cursor), 2; got != want {
		t.Fatalf("cursor: expected %d entries, got %d", want, got)
	}

	// Data round-trip.
	errorEntry := all[0]
	if errorEntry.Data == nil {
		t.Fatal("expected Data map to be populated")
	}
	if errorEntry.Data["code"] != float64(500) {
		t.Errorf("unexpected data value: %v", errorEntry.Data["code"])
	}
}

func TestInsertLogEntriesCapEnforcement(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	projectID := store.defaultProjectID

	// Insert 10,005 entries in two batches to trigger cap enforcement.
	const total = 10005
	batch := make([]LogEntry, total)
	for i := range batch {
		batch[i] = LogEntry{
			ProjectID:  projectID,
			ReceivedAt: time.Now().UTC(),
			LevelNum:   30,
			Level:      "info",
			Message:    "log line",
		}
	}
	if err := store.InsertLogEntries(ctx, batch); err != nil {
		t.Fatalf("InsertLogEntries: %v", err)
	}

	all, err := store.ListLogEntries(ctx, projectID, 0, "", 10001, 0)
	if err != nil {
		t.Fatalf("ListLogEntries: %v", err)
	}
	if len(all) > 10000 {
		t.Errorf("cap not enforced: got %d entries, expected <= 10000", len(all))
	}
}
