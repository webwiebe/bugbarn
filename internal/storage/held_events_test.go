package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestHeldEventsCRUD(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	proj, err := store.CreateProjectPending(ctx, "svc")
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := store.HoldEvent(ctx, HeldRecord{
			ProjectID:   proj.ID,
			Slug:        "svc",
			Kind:        HeldKindEvent,
			IngestID:    "ing",
			ReceivedAt:  time.Now().UTC(),
			ContentType: "application/json",
			BodyBase64:  "eyJ4IjoxfQ==",
		}); err != nil {
			t.Fatalf("hold %d: %v", i, err)
		}
	}

	n, err := store.CountHeldByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}

	held, err := store.ListHeldByProject(ctx, proj.ID, 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("list returned %d, want 2 (limit honored)", len(held))
	}
	// Oldest first.
	if held[0].ID > held[1].ID {
		t.Errorf("not ordered oldest-first: %d then %d", held[0].ID, held[1].ID)
	}
	if held[0].Kind != HeldKindEvent || held[0].BodyBase64 != "eyJ4IjoxfQ==" {
		t.Errorf("unexpected held record: %+v", held[0])
	}

	if err := store.DeleteHeldEvent(ctx, held[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	n, _ = store.CountHeldByProject(ctx, proj.ID)
	if n != 2 {
		t.Errorf("count after delete = %d, want 2", n)
	}
}

func TestHoldEventFailsOnReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bugbarn.db")
	// Create and migrate the DB first.
	rw, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	rw.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	t.Cleanup(func() { ro.Close() })

	err = ro.HoldEvent(context.Background(), HeldRecord{ProjectID: 1, Slug: "x", Kind: HeldKindEvent, ReceivedAt: time.Now().UTC(), BodyBase64: "e30="})
	if err == nil {
		t.Fatal("expected HoldEvent to fail on read-only store")
	}
}
