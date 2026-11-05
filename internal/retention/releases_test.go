package retention

import (
	"context"
	"errors"
	"testing"
)

// The release pass deletes the excess in batches and stops on the short batch.
func TestSweepReleasesPrunesTheExcessInBatches(t *testing.T) {
	tun := fastTuning()
	store := &fakeStore{releaseExcess: int64(tun.batchSize)*2 + 5}
	sweepReleases(context.Background(), store, tun, quietLogger())

	if store.releaseExcess != 0 {
		t.Fatalf("excess left = %d, want 0", store.releaseExcess)
	}
	if len(store.releaseCalls) != 3 {
		t.Fatalf("batches = %d, want 3", len(store.releaseCalls))
	}
	for i, keep := range store.releaseCalls {
		if keep != ReleasesPerProject {
			t.Errorf("batch %d kept %d per project, want %d", i, keep, ReleasesPerProject)
		}
	}
}

// One sweep runs at most maxReleaseBatches batches; the rest waits for the next.
func TestSweepReleasesIsBoundedPerSweep(t *testing.T) {
	tun := fastTuning()
	store := &fakeStore{releaseExcess: int64(tun.batchSize) * (maxReleaseBatches + 10)}
	sweepReleases(context.Background(), store, tun, quietLogger())

	if len(store.releaseCalls) != maxReleaseBatches {
		t.Fatalf("batches = %d, want %d", len(store.releaseCalls), maxReleaseBatches)
	}
}

// A failed batch ends the pass without retrying in the same sweep.
func TestSweepReleasesStopsOnError(t *testing.T) {
	store := &fakeStore{releaseExcess: 10, releaseErr: errors.New("disk on fire")}
	sweepReleases(context.Background(), store, fastTuning(), quietLogger())

	if len(store.releaseCalls) != 1 {
		t.Fatalf("batches = %d, want 1", len(store.releaseCalls))
	}
}

// A sweep that starts during shutdown does nothing.
func TestSweepReleasesSkipsWhenShuttingDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{releaseExcess: 10}
	sweepReleases(ctx, store, fastTuning(), quietLogger())

	if len(store.releaseCalls) != 0 {
		t.Fatalf("batches = %d, want 0", len(store.releaseCalls))
	}
}
