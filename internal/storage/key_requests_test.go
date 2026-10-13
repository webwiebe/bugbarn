package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func seedKeyRequest(t *testing.T, s *Store, id string, created time.Time) domain.Project {
	t.Helper()
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	kr := domain.KeyRequest{ID: id, ProjectID: proj.ID, Label: "org/repo prod", CreatedAt: created, ExpiresAt: created.Add(24 * time.Hour)}
	if err := s.CreateKeyRequest(ctx, kr, "pollhash"); err != nil {
		t.Fatal(err)
	}
	return proj
}

func TestKeyRequestClaimCreatesOneIngestKey(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proj := seedKeyRequest(t, s, "r1", now)

	// Not approved yet: a claim fails and mints nothing.
	if err := s.ClaimKeyRequest(ctx, "r1", "agent", "sha-a", now); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("claim of a pending request: %v", err)
	}
	if _, _, found, _ := s.ValidAPIKeySHA256(ctx, "sha-a"); found {
		t.Fatal("a key exists for an unapproved request")
	}

	if err := s.ResolveKeyRequest(ctx, "r1", domain.KeyRequestApproved, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimKeyRequest(ctx, "r1", "agent", "sha-b", now); err != nil {
		t.Fatal(err)
	}
	pid, scope, found, err := s.ValidAPIKeySHA256(ctx, "sha-b")
	if err != nil || !found || pid != proj.ID || scope != domain.APIKeyScopeIngest {
		t.Fatalf("key = project %d scope %q found %v err %v", pid, scope, found, err)
	}
	if err := s.ClaimKeyRequest(ctx, "r1", "agent", "sha-c", now); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
	if _, _, found, _ := s.ValidAPIKeySHA256(ctx, "sha-c"); found {
		t.Fatal("a second claim minted a second key")
	}
}

func TestKeyRequestResolveRespectsStateAndExpiry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created := time.Now().UTC()
	seedKeyRequest(t, s, "r1", created)

	if err := s.ResolveKeyRequest(ctx, "r1", domain.KeyRequestDenied, created); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveKeyRequest(ctx, "r1", domain.KeyRequestApproved, created); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("approve after deny: %v", err)
	}
	if err := s.ResolveKeyRequest(ctx, "missing", domain.KeyRequestApproved, created); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("approve of an unknown id: %v", err)
	}
}

func TestKeyRequestListCountAndPurge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created := time.Now().UTC().Add(-48 * time.Hour)
	proj := seedKeyRequest(t, s, "old", created)
	fresh := domain.KeyRequest{ID: "fresh", ProjectID: proj.ID, Label: "x", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := s.CreateKeyRequest(ctx, fresh, "h"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	list, err := s.ListPendingKeyRequests(ctx, now)
	if err != nil || len(list) != 1 || list[0].ID != "fresh" || list[0].ProjectSlug != "geo" {
		t.Fatalf("list = %+v err %v", list, err)
	}
	if n, err := s.CountPendingKeyRequests(ctx, proj.ID, now); err != nil || n != 1 {
		t.Fatalf("count = %d err %v", n, err)
	}

	if err := s.PurgeKeyRequests(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetKeyRequest(ctx, "old"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("old request after purge: %v", err)
	}
	if _, _, err := s.GetKeyRequest(ctx, "fresh"); err != nil {
		t.Fatalf("fresh request purged: %v", err)
	}
}

func TestKeyRequestWritesFailOnReadOnlyStore(t *testing.T) {
	var s *KeyRequestStore
	ctx := context.Background()
	if err := s.CreateKeyRequest(ctx, domain.KeyRequest{}, ""); err == nil {
		t.Fatal("create on a nil store succeeded")
	}
	if err := s.ResolveKeyRequest(ctx, "x", domain.KeyRequestApproved, time.Now()); err == nil {
		t.Fatal("resolve on a nil store succeeded")
	}
	if err := s.ClaimKeyRequest(ctx, "x", "n", "h", time.Now()); err == nil {
		t.Fatal("claim on a nil store succeeded")
	}
	if err := s.PurgeKeyRequests(ctx, time.Now()); err == nil {
		t.Fatal("purge on a nil store succeeded")
	}
}
