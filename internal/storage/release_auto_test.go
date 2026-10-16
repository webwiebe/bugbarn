package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openAutoReleaseStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestAutoReleaseCreatedOnceForNewVersion(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		_, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(at.Add(time.Duration(i)*time.Minute),
			map[string]any{"release": "1.4.0", "deployment.environment": "production"}, nil))
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
	}

	releases, err := store.ListReleases(ctx)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("want one automatic release, got %+v", releases)
	}
	got := releases[0]
	if got.Name != "1.4.0" || got.Version != "1.4.0" || got.Environment != "production" || got.CreatedBy != "auto" {
		t.Errorf("unexpected release %+v", got)
	}
	if !got.ObservedAt.Equal(at) {
		t.Errorf("observed_at %v, want first event time %v", got.ObservedAt, at)
	}
}

func TestAutoReleaseNewVersionAddsSecondRelease(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, v := range []string{"1.0.0", "1.1.0", "1.0.0"} {
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(at, map[string]any{"service.version": v}, nil)); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	releases, _ := store.ListReleases(ctx)
	if len(releases) != 2 {
		t.Fatalf("want two releases, got %+v", releases)
	}
}

func TestAutoReleaseReusesManuallyPostedRelease(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	if _, err := store.CreateRelease(ctx, Release{Name: "v2.0", Version: "2.0.0", CreatedBy: "ci"}); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if _, err := store.CreateRelease(ctx, Release{Name: "3.0.0", CreatedBy: "ci"}); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	for _, v := range []string{"2.0.0", "3.0.0"} {
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(time.Now().UTC(),
			map[string]any{"release": v, "deployment.environment": "production"}, nil)); err != nil {
			t.Fatalf("persist: %v", err)
		}
	}
	releases, _ := store.ListReleases(ctx)
	if len(releases) != 2 {
		t.Fatalf("manual releases must be reused (matched by version or name), got %+v", releases)
	}
}

func TestAutoReleaseSkippedWithoutReleaseValue(t *testing.T) {
	t.Parallel()
	store := openAutoReleaseStore(t)
	ctx := context.Background()
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, releaseEvent(time.Now().UTC(), map[string]any{"foo": "bar"}, nil)); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if releases, _ := store.ListReleases(ctx); len(releases) != 0 {
		t.Fatalf("an event without a release must create nothing, got %+v", releases)
	}
}
