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

	remaining   int64 // rows still older than the cutoff
	limits      []int // limit passed to each DeleteEventsBefore call
	cutoffs     []time.Time
	deleteErr   error
	countErr    error
	countCalls  int
	deleteDelay time.Duration // simulates a batch that holds the writer
}

func (f *fakeStore) DeleteEventsBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = append(f.limits, limit)
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.deleteDelay > 0 {
		time.Sleep(f.deleteDelay)
	}
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
	return tuning{
		batchSize:   batchSize,
		minPause:    0,
		maxPause:    0,
		budget:      sweepBudget,
		maxPerSweep: maxDeletesPerSweep,
	}
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

// CountEventsBefore is a full scan of events — no index spans received_at
// across projects. A sweep that ends on a short batch has already proved the
// backlog is empty, so counting anyway bought a whole-table scan every hour to
// log a zero. Production did exactly that on every steady-state sweep.
func TestSweepSkipsBacklogCountWhenDrained(t *testing.T) {
	store := &fakeStore{remaining: batchSize + 10}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if store.remaining != 0 {
		t.Fatalf("remaining = %d, want 0 — the sweep should have drained it", store.remaining)
	}
	if store.countCalls != 0 {
		t.Errorf("CountEventsBefore called %d times after a drained sweep, want 0 — "+
			"a short batch already proves the backlog is empty", store.countCalls)
	}
}

// The converse: when a budget cuts the sweep short the backlog really is
// unknown, so the count must still run or "still draining" becomes invisible.
func TestSweepCountsBacklogWhenBudgetStopsIt(t *testing.T) {
	store := &fakeStore{remaining: maxDeletesPerSweep * 3}
	sweep(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if store.remaining == 0 {
		t.Fatal("budget did not bound the sweep")
	}
	if store.countCalls != 1 {
		t.Errorf("CountEventsBefore called %d times after a budget-stopped sweep, want 1", store.countCalls)
	}
}

// A batch that holds the writer for seconds must be followed by a comparable
// yield, or ingest gets a sliver of the connection and log inserts get dropped
// (BS2-98).
func TestPauseIsProportionalToBatchCost(t *testing.T) {
	tun := defaultTuning()

	if got := pauseFor(time.Millisecond, tun); got != tun.minPause {
		t.Errorf("fast batch paused %v, want the %v floor", got, tun.minPause)
	}
	slow := 5 * time.Second
	if got := pauseFor(slow, tun); got != slow {
		t.Errorf("a %v batch paused %v, want %v — the sweep must yield what it consumed", slow, got, slow)
	}
	if got := pauseFor(time.Hour, tun); got != tun.maxPause {
		t.Errorf("pathological batch paused %v, want the %v cap", got, tun.maxPause)
	}
}

// The row budget cannot bound wall clock unless you know what a row costs, and
// production showed we did not: batches ran tens of seconds, so a full row
// budget would sweep for hours and overrun the hourly interval.
func TestSweepStopsOnWallClockBudget(t *testing.T) {
	store := &fakeStore{remaining: maxDeletesPerSweep, deleteDelay: 20 * time.Millisecond}
	tun := fastTuning()
	tun.budget = 60 * time.Millisecond

	start := time.Now()
	sweep(context.Background(), store, Config{RetentionDays: 30}, tun, quietLogger(), nil)
	elapsed := time.Since(start)

	if store.remaining == 0 {
		t.Fatal("the whole backlog drained; the wall-clock budget did nothing")
	}
	// Generous ceiling: the budget is only checked between batches, so one
	// in-flight batch always overruns it.
	if elapsed > time.Second {
		t.Errorf("sweep ran %v with a %v budget — the clock is not bounding it", elapsed, tun.budget)
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
	if tun.minPause <= 0 {
		t.Error("default tuning has no inter-batch pause; retention would monopolize the writer")
	}
	if tun.maxPause < tun.minPause {
		t.Errorf("maxPause %v below minPause %v", tun.maxPause, tun.minPause)
	}
	if tun.batchSize <= 0 || tun.maxPerSweep <= 0 {
		t.Errorf("default tuning is unbounded: batchSize=%d maxPerSweep=%d", tun.batchSize, tun.maxPerSweep)
	}
	// A row budget only bounds time if you know what a row costs, and
	// production proved we did not. The wall-clock budget is what actually
	// keeps a sweep inside its hourly interval.
	if tun.budget <= 0 {
		t.Error("default tuning has no wall-clock sweep budget")
	}
	if tun.budget >= sweepInterval {
		t.Errorf("sweep budget %v does not fit inside the %v interval", tun.budget, sweepInterval)
	}
}
