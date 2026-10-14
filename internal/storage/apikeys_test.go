package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
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

func TestTouchAPIKeysAt_OnlyMovesForward(t *testing.T) {
	t.Parallel()

	rw, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	ctx := context.Background()
	proj, err := rw.CreateProject(ctx, "Test", "test")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := rw.CreateAPIKey(ctx, "a", proj.ID, "hash-a", APIKeyScopeIngest)
	b, _ := rw.CreateAPIKey(ctx, "b", proj.ID, "hash-b", APIKeyScopeIngest)

	late := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	early := late.Add(-time.Hour)
	if err := rw.TouchAPIKeysAt(ctx, []domain.APIKeyTouch{
		{KeySHA256: a.KeySHA256, At: late},
		{KeySHA256: "unknown", At: late},
	}); err != nil {
		t.Fatal(err)
	}
	// A slower reader delivers an older use of the same key.
	if err := rw.TouchAPIKeysAt(ctx, []domain.APIKeyTouch{{KeySHA256: a.KeySHA256, At: early}}); err != nil {
		t.Fatal(err)
	}

	keys, err := rw.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		switch k.ID {
		case a.ID:
			if !k.LastUsedAt.Equal(late) {
				t.Fatalf("key a last used %v, want %v (an older touch must not rewind it)", k.LastUsedAt, late)
			}
		case b.ID:
			if !k.LastUsedAt.IsZero() {
				t.Fatalf("key b was never touched, last used %v", k.LastUsedAt)
			}
		}
	}
}

func TestTouchAPIKeysAt_NeedsTheWriteConnection(t *testing.T) {
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
	if err := ro.TouchAPIKeysAt(context.Background(), nil); err == nil {
		t.Fatal("a read-only store must report that it cannot record touches")
	}
}
