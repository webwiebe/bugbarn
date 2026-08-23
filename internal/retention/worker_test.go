package retention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeStore records the batches asked of it and serves a finite backlog.
type fakeStore struct {
	mu sync.Mutex

	remaining  int64 // rows still older than the cutoff
	limits     []int // limit passed to each DeleteEventsBefore call
	cutoffs    []time.Time
	deleteErr  error
	countErr   error
	countCalls int
}

func (f *fakeStore) DeleteEventsBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = append(f.limits, limit)
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.deleteErr != nil {
		return 0, f.deleteErr
	}
	n := int64(limit)
	if n > f.remaining {
		n = f.remaining
	}
	f.remaining -= n
	return n, nil
}

func (f *fakeStore) CountEventsBefore(context.Context, time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countCalls++
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.remaining, nil
}

func (f *fakeStore) batches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.limits)
}

// fastTuning keeps the real batching and budget semantics but removes the
// inter-batch sleep, so a full-budget sweep is instant instead of ~50s.
func fastTuning() tuning {
	return tuning{batchSize: batchSize, pause: 0, maxPerSweep: maxDeletesPerSweep}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSweepDrainsBacklogInBatches(t *testing.T) {
	store := &fakeStore{remaining: batchSize*2 + 10}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	// Two full batches plus a short one that signals the backlog is drained.
	if got := store.batches(); got != 3 {
		t.Errorf("batches = %d, want 3", got)
	}
	if store.remaining != 0 {
		t.Errorf("remaining = %d, want 0", store.remaining)
	}
	for i, l := range store.limits {
		if l != batchSize {
			t.Errorf("batch %d used limit %d, want %d — batches must stay bounded", i, l, batchSize)
		}
	}
}

func TestSweepStopsOnceBacklogIsEmpty(t *testing.T) {
	store := &fakeStore{remaining: 0}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if got := store.batches(); got != 1 {
		t.Errorf("batches = %d, want 1 — an empty backlog costs exactly one probe", got)
	}
	// Nothing was deleted, so the backlog COUNT (a full scan) must be skipped.
	if store.countCalls != 0 {
		t.Errorf("CountEventsBefore called %d times on an empty sweep, want 0", store.countCalls)
	}
}

func TestSweepUsesConfiguredRetentionWindow(t *testing.T) {
	store := &fakeStore{remaining: 1}
	before := time.Now().UTC()
	sweep(context.Background(), store, Config{RetentionDays: 7}, fastTuning(), quietLogger(), nil)

	if len(store.cutoffs) == 0 {
		t.Fatal("no cutoff recorded")
	}
	want := before.AddDate(0, 0, -7)
	if diff := store.cutoffs[0].Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("cutoff = %v, want ~%v (7 days back)", store.cutoffs[0], want)
	}
}

func TestSweepStopsOnDeleteError(t *testing.T) {
	store := &fakeStore{remaining: batchSize * 10, deleteErr: errors.New("disk on fire")}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if got := store.batches(); got != 1 {
		t.Errorf("batches = %d, want 1 — a failing sweep must not spin", got)
	}
}

// A sweep interrupted by shutdown must abandon the loop rather than keep
// hammering a database that is going away.
func TestSweepStopsWhenContextCanceled(t *testing.T) {
	store := &fakeStore{remaining: batchSize * 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sweep(ctx, store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if got := store.batches(); got != 0 {
		t.Errorf("batches = %d, want 0 on an already-canceled context", got)
	}
}

func TestSweepRespectsPerSweepBudget(t *testing.T) {
	// A backlog far larger than one sweep's budget.
	store := &fakeStore{remaining: maxDeletesPerSweep * 3}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	deleted := int64(maxDeletesPerSweep*3) - store.remaining
	if deleted > maxDeletesPerSweep {
		t.Errorf("deleted %d in one sweep, want at most %d", deleted, maxDeletesPerSweep)
	}
	if store.remaining == 0 {
		t.Error("budget did not bound the sweep — the whole backlog drained in one run")
	}
}

func TestShuttingDownClassifiesContextErrors(t *testing.T) {
	if !shuttingDown(context.Canceled) {
		t.Error("context.Canceled should count as shutdown, not a fault")
	}
	if !shuttingDown(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should count as shutdown, not a fault")
	}
	if shuttingDown(errors.New("real failure")) {
		t.Error("a real failure must not be classified as shutdown")
	}
}

func TestStartWorkerDefaultsRetentionDaysAndStops(t *testing.T) {
	store := &fakeStore{remaining: 1}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// RetentionDays 0 must fall back to the default, never to "delete
	// everything" (a zero-day window would expire live data).
	StartWorker(ctx, store, Config{RetentionDays: 0}, quietLogger(), &wg)

	deadline := time.After(2 * time.Second)
	for store.batches() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker never ran its startup sweep")
		case <-time.After(5 * time.Millisecond):
		}
	}

	store.mu.Lock()
	cutoff := store.cutoffs[0]
	store.mu.Unlock()
	want := time.Now().UTC().AddDate(0, 0, -DefaultRetentionDays)
	if diff := cutoff.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Errorf("cutoff = %v, want ~%v (the default window)", cutoff, want)
	}

	cancel()
	wg.Wait() // must return promptly; a leak here blocks graceful shutdown
}

func TestStartWorkerIgnoresNilStore(t *testing.T) {
	var wg sync.WaitGroup
	StartWorker(context.Background(), nil, Config{}, quietLogger(), &wg)
	wg.Wait() // no goroutine should have been registered
}

// The pause is the only thing bounding retention's share of the single write
// connection. Removing it would let a large backlog starve ingest, so pin it.
func TestDefaultTuningThrottlesBetweenBatches(t *testing.T) {
	tun := defaultTuning()
	if tun.pause <= 0 {
		t.Error("default tuning has no inter-batch pause; retention would monopolize the writer")
	}
	if tun.batchSize <= 0 || tun.maxPerSweep <= 0 {
		t.Errorf("default tuning is unbounded: batchSize=%d maxPerSweep=%d", tun.batchSize, tun.maxPerSweep)
	}
}
