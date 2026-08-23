package projects

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// usageFetcher is a stand-in for the expensive repository aggregate: it counts
// how many times it actually ran, which is the whole point of the cache.
type usageFetcher struct {
	mu    sync.Mutex
	calls int
	value map[int64]storage.ProjectUsage
	err   error
	delay time.Duration
}

func (f *usageFetcher) fetch(context.Context) (map[int64]storage.ProjectUsage, error) {
	f.mu.Lock()
	f.calls++
	err, val, delay := f.err, f.value, f.delay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (f *usageFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func usageOf(events int) map[int64]storage.ProjectUsage {
	return map[int64]storage.ProjectUsage{1: {ProjectID: 1, EventCount: events}}
}

func TestUsageCacheServesWithinTTL(t *testing.T) {
	f := &usageFetcher{value: usageOf(10)}
	var c usageCache
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		got, stale, err := c.get(ctx, f.fetch)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if stale {
			t.Error("fresh value reported as stale")
		}
		if got[1].EventCount != 10 {
			t.Errorf("EventCount = %d, want 10", got[1].EventCount)
		}
	}
	if f.callCount() != 1 {
		t.Errorf("underlying query ran %d times, want 1 — the cache is not holding", f.callCount())
	}
}

func TestUsageCacheRecomputesAfterTTL(t *testing.T) {
	f := &usageFetcher{value: usageOf(10)}
	var c usageCache
	ctx := context.Background()

	if _, _, err := c.get(ctx, f.fetch); err != nil {
		t.Fatalf("first get: %v", err)
	}
	// Age the entry past its TTL without waiting a real minute.
	c.mu.Lock()
	c.fetched = time.Now().Add(-usageTTL - time.Second)
	c.mu.Unlock()

	f.mu.Lock()
	f.value = usageOf(99)
	f.mu.Unlock()

	got, _, err := c.get(ctx, f.fetch)
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if got[1].EventCount != 99 {
		t.Errorf("EventCount = %d, want 99 — an expired entry must refresh", got[1].EventCount)
	}
	if f.callCount() != 2 {
		t.Errorf("underlying query ran %d times, want 2", f.callCount())
	}
}

// The failure this replaces: the handler discarded the error and rendered an
// empty map as zeros. With nothing cached there is nothing honest to serve, so
// the error must surface.
func TestUsageCacheReturnsErrorWhenNothingCached(t *testing.T) {
	want := errors.New("query timed out")
	f := &usageFetcher{err: want}
	var c usageCache

	got, stale, err := c.get(context.Background(), f.fetch)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if got != nil {
		t.Errorf("got = %v, want nil — an empty map would be read as real zeros", got)
	}
	if stale {
		t.Error("stale should be false when there is no cached value")
	}
}

func TestUsageCacheServesStaleOnRefreshFailure(t *testing.T) {
	f := &usageFetcher{value: usageOf(42)}
	var c usageCache
	ctx := context.Background()

	if _, _, err := c.get(ctx, f.fetch); err != nil {
		t.Fatalf("priming get: %v", err)
	}

	c.mu.Lock()
	c.fetched = time.Now().Add(-usageTTL - time.Second)
	c.mu.Unlock()
	f.mu.Lock()
	f.err = errors.New("database unavailable")
	f.mu.Unlock()

	got, stale, err := c.get(ctx, f.fetch)
	if err != nil {
		t.Fatalf("err = %v, want nil — a stale-but-real value beats failing", err)
	}
	if !stale {
		t.Error("stale = false, want true so the caller can say the number is old")
	}
	if got[1].EventCount != 42 {
		t.Errorf("EventCount = %d, want the last known 42", got[1].EventCount)
	}
}

func TestUsageCacheInvalidateForcesRecompute(t *testing.T) {
	f := &usageFetcher{value: usageOf(1)}
	var c usageCache
	ctx := context.Background()

	if _, _, err := c.get(ctx, f.fetch); err != nil {
		t.Fatalf("priming get: %v", err)
	}
	c.invalidate()
	if _, _, err := c.get(ctx, f.fetch); err != nil {
		t.Fatalf("post-invalidate get: %v", err)
	}
	if f.callCount() != 2 {
		t.Errorf("underlying query ran %d times, want 2 — invalidate did not take", f.callCount())
	}
}

// Before the cache, a burst of concurrent callers each started its own
// full-table scan and they timed each other out. One query must serve them all.
func TestUsageCacheCoalescesConcurrentCallers(t *testing.T) {
	f := &usageFetcher{value: usageOf(7), delay: 50 * time.Millisecond}
	var c usageCache
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := c.get(ctx, f.fetch); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}
	wg.Wait()

	if f.callCount() != 1 {
		t.Errorf("underlying query ran %d times for 20 concurrent callers, want 1", f.callCount())
	}
}

// The refresh is shared, and in production the callers that trigger it give up
// well before the query finishes. If the refresh inherited the caller's
// cancellation, every refill would abort and the cache would never populate —
// which is exactly the failure it exists to fix.
func TestUsageCacheRefreshSurvivesCallerCancellation(t *testing.T) {
	var c usageCache

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller has already given up

	var sawCancellation bool
	fetch := func(fetchCtx context.Context) (map[int64]storage.ProjectUsage, error) {
		if err := fetchCtx.Err(); err != nil {
			sawCancellation = true
			return nil, err
		}
		return usageOf(5), nil
	}

	got, _, err := c.get(ctx, fetch)
	if sawCancellation {
		t.Fatal("refresh inherited the caller's cancellation; the cache would never fill")
	}
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got[1].EventCount != 5 {
		t.Errorf("EventCount = %d, want 5", got[1].EventCount)
	}
	// And the result is cached, so the caller that did wait gets it instantly.
	if c.fetched.IsZero() {
		t.Error("successful refresh was not cached")
	}
}

// Detaching from the caller must not mean running forever.
func TestUsageCacheRefreshIsBounded(t *testing.T) {
	var c usageCache

	var deadlineSet bool
	fetch := func(fetchCtx context.Context) (map[int64]storage.ProjectUsage, error) {
		_, deadlineSet = fetchCtx.Deadline()
		return usageOf(1), nil
	}
	if _, _, err := c.get(context.Background(), fetch); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !deadlineSet {
		t.Error("refresh context has no deadline; a wedged read would pin the cache mutex")
	}
}

func TestServiceUsageAllSurfacesRepositoryError(t *testing.T) {
	repo := &fakeRepo{err: errors.New("boom")}
	svc := New(repo, nil)

	usage, stale, err := svc.UsageAll(context.Background())
	if err == nil {
		t.Fatal("expected the repository error to surface, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on error", usage)
	}
	if stale {
		t.Error("stale should be false when the call failed outright")
	}
}
