package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTouchAPIKey_ReadOnlyStore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")

	rw, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	proj, err := rw.CreateProject(ctx, "Test", "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rw.CreateAPIKey(ctx, "k1", proj.ID, "abc123hash", APIKeyScopeIngest)
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Must not panic and must return nil on a read-only store.
	if err := ro.TouchAPIKey(ctx, key.KeySHA256); err != nil {
		t.Fatalf("TouchAPIKey on read-only store returned unexpected error: %v", err)
	}
}

func TestTouchAPIKey_ReadWriteStore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	rw, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()

	ctx := context.Background()
	proj, err := rw.CreateProject(ctx, "Test", "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rw.CreateAPIKey(ctx, "k1", proj.ID, "abc123hash", APIKeyScopeIngest)
	if err != nil {
		t.Fatal(err)
	}

	if err := rw.TouchAPIKey(ctx, key.KeySHA256); err != nil {
		t.Fatalf("TouchAPIKey on read-write store: %v", err)
	}

	// Touching a non-existent key is a no-op, not an error.
	if err := rw.TouchAPIKey(ctx, "doesnotexist"); err != nil {
		t.Fatalf("TouchAPIKey for unknown key: %v", err)
	}
}
