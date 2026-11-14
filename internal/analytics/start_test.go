package analytics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// cutoffStore signals once the first rollup pass reaches its last step.
type cutoffStore struct {
	fakeStore
	once   sync.Once
	cutoff chan time.Time
}

func (c *cutoffStore) DeleteOldPageviews(ctx context.Context, cutoff time.Time) error {
	c.once.Do(func() { c.cutoff <- cutoff })
	return c.fakeStore.DeleteOldPageviews(ctx, cutoff)
}

func TestStartWorkerRunsInitialRollupAndStopsOnCancel(t *testing.T) {
	store := &cutoffStore{fakeStore: fakeStore{projectIDs: ids(2)}, cutoff: make(chan time.Time, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	StartWorker(ctx, store, 30, &wg)
	cutoff := <-store.cutoff
	cancel()
	wg.Wait()

	if got := store.rollupCount(); got != 4 {
		t.Errorf("rollups = %d, want 4 (2 projects x 2 dates)", got)
	}
	wantCutoff := time.Now().UTC().AddDate(0, 0, -30)
	if d := wantCutoff.Sub(cutoff); d < 0 || d > time.Minute {
		t.Errorf("retention cutoff = %v, want about 30 days back (%v)", cutoff, wantCutoff)
	}
}

func TestStartWorkerDefaultsRetentionTo90Days(t *testing.T) {
	store := &cutoffStore{cutoff: make(chan time.Time, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	StartWorker(ctx, store, 0, nil)
	cutoff := <-store.cutoff
	cancel()

	wantCutoff := time.Now().UTC().AddDate(0, 0, -90)
	if d := wantCutoff.Sub(cutoff); d < 0 || d > time.Minute {
		t.Errorf("retention cutoff = %v, want about 90 days back", cutoff)
	}
}

func TestRunRollupLogsRetentionFailure(t *testing.T) {
	logs := withErrorCounter(t)
	runRollup(context.Background(), &fakeStore{deleteErr: context.DeadlineExceeded}, 90)
	if logs.errors() != 0 {
		t.Errorf("a deadline during retention logged %d errors, want 0", logs.errors())
	}
	runRollup(context.Background(), &fakeStore{deleteErr: errBoom}, 90)
	if logs.errors() != 1 {
		t.Errorf("a real retention failure logged %d errors, want 1", logs.errors())
	}
}

func TestRunRollupLogsListFailure(t *testing.T) {
	logs := withErrorCounter(t)
	runRollup(context.Background(), &fakeStore{listErr: errBoom}, 90)
	if logs.errors() != 1 {
		t.Errorf("list failure logged %d errors, want 1", logs.errors())
	}
}
