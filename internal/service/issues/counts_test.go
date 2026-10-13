package issues

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func newCountsService(repo *fakeRepo) *Service {
	return New(repo, slog.New(slog.DiscardHandler))
}

func TestCountsServedFromCacheWithinTTL(t *testing.T) {
	repo := &fakeRepo{counts: domain.IssueCounts{Open: 3, All: 5}}
	svc := newCountsService(repo)
	ctx := domain.WithProjectID(context.Background(), 7)

	for range 3 {
		got, err := svc.Counts(ctx)
		if err != nil || got.Open != 3 || got.All != 5 {
			t.Fatalf("counts = %+v err %v", got, err)
		}
	}
	if repo.countCalls != 1 {
		t.Fatalf("repo hit %d times, want 1", repo.countCalls)
	}
}

func TestCountsAreCachedPerScope(t *testing.T) {
	repo := &fakeRepo{}
	svc := newCountsService(repo)
	base := context.Background()

	_, _ = svc.Counts(domain.WithProjectID(base, 1))
	_, _ = svc.Counts(domain.WithProjectID(base, 2))
	_, _ = svc.Counts(base)
	_, _ = svc.Counts(domain.WithProjectIDs(base, []int64{3, 4}))
	_, _ = svc.Counts(domain.WithProjectIDs(base, []int64{4, 3}))
	if repo.countCalls != 4 {
		t.Fatalf("repo hit %d times, want 4 (group order must not matter)", repo.countCalls)
	}
}

func TestCountsRefreshAfterTTL(t *testing.T) {
	repo := &fakeRepo{counts: domain.IssueCounts{All: 1}}
	svc := newCountsService(repo)
	ctx := domain.WithProjectID(context.Background(), 1)
	_, _ = svc.Counts(ctx)

	svc.counts.mu.Lock()
	e := svc.counts.entries["project:1"]
	e.fetched = time.Now().Add(-2 * countsTTL)
	svc.counts.entries["project:1"] = e
	svc.counts.mu.Unlock()

	repo.counts = domain.IssueCounts{All: 2}
	got, _ := svc.Counts(ctx)
	if got.All != 2 || repo.countCalls != 2 {
		t.Fatalf("counts = %+v, calls %d", got, repo.countCalls)
	}
}

func TestStatusChangeDropsTheCache(t *testing.T) {
	repo := &fakeRepo{issues: map[string]domain.Issue{"BB-1": {ID: "BB-1"}}, counts: domain.IssueCounts{Open: 1, All: 1}}
	svc := newCountsService(repo)
	ctx := domain.WithProjectID(context.Background(), 1)
	_, _ = svc.Counts(ctx)

	repo.counts = domain.IssueCounts{Resolved: 1, All: 1}
	if _, err := svc.Resolve(ctx, "BB-1"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Counts(ctx)
	if got.Resolved != 1 || got.Open != 0 {
		t.Fatalf("counts after resolve = %+v", got)
	}
}

func TestCountsServeStaleWhenRefreshFails(t *testing.T) {
	repo := &fakeRepo{counts: domain.IssueCounts{All: 9}}
	svc := newCountsService(repo)
	ctx := domain.WithProjectID(context.Background(), 1)
	_, _ = svc.Counts(ctx)

	svc.counts.mu.Lock()
	e := svc.counts.entries["project:1"]
	e.fetched = time.Now().Add(-2 * countsTTL)
	svc.counts.entries["project:1"] = e
	svc.counts.mu.Unlock()

	repo.countsErr = errors.New("database is locked")
	got, err := svc.Counts(ctx)
	if err != nil || got.All != 9 {
		t.Fatalf("stale counts = %+v err %v", got, err)
	}
}

func TestCountsErrorWithoutCacheIsReturned(t *testing.T) {
	repo := &fakeRepo{countsErr: errors.New("boom")}
	svc := newCountsService(repo)
	if _, err := svc.Counts(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}
