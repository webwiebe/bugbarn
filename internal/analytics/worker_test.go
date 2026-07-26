package analytics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// errorLogCounter counts records emitted at ERROR level.
type errorLogCounter struct {
	mu    sync.Mutex
	count int
}

func (h *errorLogCounter) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelError
}

func (h *errorLogCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.mu.Lock()
		h.count++
		h.mu.Unlock()
	}
	return nil
}

func (h *errorLogCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *errorLogCounter) WithGroup(string) slog.Handler      { return h }

func (h *errorLogCounter) errors() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

type fakeStore struct {
	projectIDs []int64
	listErr    error
	rollupErr  error
	deleteErr  error

	mu       sync.Mutex
	rollups  int
	deletes  int
	rolledUp []int64
}

func (f *fakeStore) ListProjectIDs(context.Context) ([]int64, error) {
	return f.projectIDs, f.listErr
}

func (f *fakeStore) RollupDailyAnalytics(_ context.Context, projectID int64, _ time.Time) error {
	f.mu.Lock()
	f.rollups++
	f.rolledUp = append(f.rolledUp, projectID)
	f.mu.Unlock()
	return f.rollupErr
}

func (f *fakeStore) DeleteOldPageviews(context.Context, time.Time) error {
	f.mu.Lock()
	f.deletes++
	f.mu.Unlock()
	return f.deleteErr
}

func (f *fakeStore) rollupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rollups
}

func ids(n int) []int64 {
	out := make([]int64, 0, n)
	for i := range n {
		out = append(out, int64(i+1))
	}
	return out
}

// withErrorCounter swaps the default slog logger for the duration of a test,
// since runRollup logs through the package-level slog.
func withErrorCounter(t *testing.T) *errorLogCounter {
	t.Helper()
	h := &errorLogCounter{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// A shutdown mid-rollup is expected, not a fault. Logging it at ERROR made
// selflog file one bug per project per date on every deploy.
func TestRunRollupDoesNotLogShutdownAsError(t *testing.T) {
	logs := withErrorCounter(t)
	store := &fakeStore{projectIDs: ids(50), rollupErr: context.Canceled, deleteErr: context.Canceled}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runRollup(ctx, store, 90)

	if got := logs.errors(); got != 0 {
		t.Errorf("shutdown produced %d ERROR logs, want 0", got)
	}
}

// Once the context is done, walking the remaining projects only produces
// guaranteed failures — stop instead.
func TestRunRollupStopsEarlyOnCancel(t *testing.T) {
	withErrorCounter(t)
	store := &fakeStore{projectIDs: ids(50), rollupErr: context.Canceled}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runRollup(ctx, store, 90)

	if got := store.rollupCount(); got != 0 {
		t.Errorf("want no rollup attempts on an already-cancelled context, got %d", got)
	}
}

// A genuine rollup failure must still be reported, and must not abort the
// rest of the fleet.
func TestRunRollupLogsRealErrorsAndContinues(t *testing.T) {
	logs := withErrorCounter(t)
	store := &fakeStore{projectIDs: ids(3), rollupErr: errors.New("table is gone")}

	runRollup(context.Background(), store, 90)

	// 3 projects x 2 dates, all failing.
	if got, want := logs.errors(), 6; got != want {
		t.Errorf("got %d ERROR logs, want %d", got, want)
	}
	if got, want := store.rollupCount(), 6; got != want {
		t.Errorf("got %d rollup attempts, want %d (must not abort early)", got, want)
	}
}

func TestRunRollupListFailureShutdownIsQuiet(t *testing.T) {
	logs := withErrorCounter(t)
	store := &fakeStore{listErr: context.Canceled}

	runRollup(context.Background(), store, 90)

	if got := logs.errors(); got != 0 {
		t.Errorf("cancelled project list produced %d ERROR logs, want 0", got)
	}

	logs2 := withErrorCounter(t)
	store2 := &fakeStore{listErr: errors.New("db down")}

	runRollup(context.Background(), store2, 90)

	if got := logs2.errors(); got != 1 {
		t.Errorf("real list failure produced %d ERROR logs, want 1", got)
	}
}
