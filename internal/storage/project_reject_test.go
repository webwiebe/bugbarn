package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Deleting a pending project is how an admin rejects it (spec 005 FR-505): the
// row, its held ingest payloads and its key requests go, and nothing is left
// behind that a later project could inherit.
func TestDeletePendingProjectRejectsIt(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	proj, err := store.CreateProjectPending(ctx, "funnelbarni")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HoldEvent(ctx, HeldRecord{
		ProjectID:  proj.ID,
		Slug:       "funnelbarni",
		Kind:       HeldKindEvent,
		ReceivedAt: time.Now().UTC(),
		BodyBase64: "eyJ4IjoxfQ==",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateKeyRequest(ctx, domain.KeyRequest{
		ID: "kr-1", ProjectID: proj.ID, Label: "typo", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, "pollhash"); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteProject(ctx, "funnelbarni"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ProjectBySlug(ctx, "funnelbarni"); err == nil {
		t.Fatal("project still resolves after delete")
	}
	if n, err := store.CountHeldByProject(ctx, proj.ID); err != nil || n != 0 {
		t.Fatalf("held events after delete = %d, err %v", n, err)
	}
	if _, _, err := store.GetKeyRequest(ctx, "kr-1"); err == nil {
		t.Fatal("key request survived the project delete")
	}
	pending, err := store.ListPendingKeyRequests(ctx, now)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending key requests = %+v, err %v", pending, err)
	}
}
