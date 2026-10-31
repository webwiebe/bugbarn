package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func openRevocationStore(t *testing.T) (*Store, domain.Project) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	proj, err := store.CreateProject(context.Background(), "Alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	return store, proj
}

func keyRowCount(t *testing.T, store *Store, sha string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT count(*) FROM api_keys WHERE key_sha256 = ?`, sha).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRevokeSetupKeyRemovesTheRowAndKeepsItGone(t *testing.T) {
	t.Parallel()
	store, proj := openRevocationStore(t)
	ctx := context.Background()

	if err := store.EnsureSetupAPIKey(ctx, "alpha-setup", proj.ID, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if got := keyRowCount(t, store, "hash-a"); got != 1 {
		t.Fatalf("rows before revoking = %d, want 1", got)
	}
	if revoked, err := store.SetupKeyRevoked(ctx, "alpha"); err != nil || revoked {
		t.Fatalf("before revoking: revoked=%v err=%v", revoked, err)
	}

	if err := store.RevokeSetupKey(ctx, "alpha", "hash-a"); err != nil {
		t.Fatal(err)
	}
	if got := keyRowCount(t, store, "hash-a"); got != 0 {
		t.Fatalf("rows after revoking = %d, want 0", got)
	}
	if revoked, err := store.SetupKeyRevoked(ctx, "alpha"); err != nil || !revoked {
		t.Fatalf("after revoking: revoked=%v err=%v", revoked, err)
	}

	// The setup page does not bring the row back.
	if err := store.EnsureSetupAPIKey(ctx, "alpha-setup", proj.ID, "hash-a"); err != nil {
		t.Fatal(err)
	}
	if got := keyRowCount(t, store, "hash-a"); got != 0 {
		t.Fatalf("rows after the setup page = %d, want 0", got)
	}
}

func TestRevokeSetupKeyWorksWithoutARowOrAProject(t *testing.T) {
	t.Parallel()
	store, _ := openRevocationStore(t)
	ctx := context.Background()

	if err := store.RevokeSetupKey(ctx, "no-such-project", "hash-x"); err != nil {
		t.Fatal(err)
	}
	if revoked, err := store.SetupKeyRevoked(ctx, "no-such-project"); err != nil || !revoked {
		t.Fatalf("revoked=%v err=%v", revoked, err)
	}
}

func TestRevokeSetupKeyTwiceKeepsTheFirstTime(t *testing.T) {
	t.Parallel()
	store, _ := openRevocationStore(t)
	ctx := context.Background()

	if err := store.RevokeSetupKey(ctx, "alpha", "hash-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE setup_key_revocations SET revoked_at = '2026-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSetupKey(ctx, "alpha", "hash-a"); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListSetupKeyRevocations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != "alpha" || list[0].RevokedAt.Year() != 2026 || list[0].RevokedAt.Month() != time.January {
		t.Fatalf("revocations = %+v, want alpha at the first time", list)
	}
}

// A reader can run a new binary before the writer has applied the migration.
func TestSetupKeyRevokedReadsAMissingTableAsNotRevoked(t *testing.T) {
	t.Parallel()
	store, _ := openRevocationStore(t)
	ctx := context.Background()
	if _, err := store.db.Exec(`DROP TABLE setup_key_revocations`); err != nil {
		t.Fatal(err)
	}
	if revoked, err := store.SetupKeyRevoked(ctx, "alpha"); err != nil || revoked {
		t.Fatalf("revoked=%v err=%v", revoked, err)
	}
	if list, err := store.ListSetupKeyRevocations(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestSetupKeyRevokedReportsOtherReadErrors(t *testing.T) {
	t.Parallel()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := store.SetupKeyRevoked(context.Background(), "alpha"); err == nil {
		t.Fatal("a closed database must report an error, never a verdict")
	}
}

func TestSnapshotSettingsKeepsRevocations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bugbarn.db")
	src, err := Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx := context.Background()
	if err := src.RevokeSetupKey(ctx, "alpha", "hash-a"); err != nil {
		t.Fatal(err)
	}

	destPath := filepath.Join(dir, "settings.db")
	if _, err := SnapshotSettings(ctx, srcPath, destPath); err != nil {
		t.Fatal(err)
	}
	snap, err := Open(destPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if revoked, err := snap.SetupKeyRevoked(ctx, "alpha"); err != nil || !revoked {
		t.Fatalf("a restore lost the revocation: revoked=%v err=%v", revoked, err)
	}
}
