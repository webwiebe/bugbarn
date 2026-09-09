package retention

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A project with a shorter window gets its own pass, with its own cutoff.
func TestSweepProjectOverridesUsesTheProjectWindow(t *testing.T) {
	store := &fakeStore{
		overrides:     map[int64]int{7: 3},
		projectRemain: map[int64]int64{7: 250},
	}

	sweepProjectOverrides(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if got := store.projectDeletes[7]; got != 250 {
		t.Errorf("deleted %d rows for project 7, want 250", got)
	}
	cutoffs := store.projectCutoffs[7]
	if len(cutoffs) == 0 {
		t.Fatal("project 7 was never swept")
	}
	// The cutoff must be the project's 3 days, not the deployment's 30.
	age := time.Since(cutoffs[0])
	if age < 2*24*time.Hour || age > 4*24*time.Hour {
		t.Errorf("cutoff is %v old, want roughly 3 days (the project window, not the global one)", age)
	}
}

// A window at or beyond the global one is meaningless: the global pass has
// already deleted everything past the deployment window, so honoring it would
// promise data we do not have.
func TestSweepProjectOverridesIgnoresWindowsNotShorterThanGlobal(t *testing.T) {
	store := &fakeStore{
		overrides:     map[int64]int{1: 30, 2: 90, 3: 5},
		projectRemain: map[int64]int64{1: 100, 2: 100, 3: 100},
	}

	sweepProjectOverrides(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if got := store.projectDeletes[1]; got != 0 {
		t.Errorf("project 1 (window == global) deleted %d rows, want 0", got)
	}
	if got := store.projectDeletes[2]; got != 0 {
		t.Errorf("project 2 (window > global) deleted %d rows, want 0", got)
	}
	if got := store.projectDeletes[3]; got != 100 {
		t.Errorf("project 3 (window < global) deleted %d rows, want 100", got)
	}
}

func TestSweepProjectOverridesNoOverridesIsANoop(t *testing.T) {
	store := &fakeStore{}
	sweepProjectOverrides(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)
	if len(store.projectCutoffs) != 0 {
		t.Errorf("swept %d projects with no overrides configured", len(store.projectCutoffs))
	}
}

// Reading the overrides is best effort: a failure must not take the sweep down,
// because the global pass has already done the important work by then.
func TestSweepProjectOverridesSurvivesAListingFailure(t *testing.T) {
	store := &fakeStore{overridesErr: errors.New("database is locked")}
	sweepProjectOverrides(context.Background(), store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)
	if len(store.projectCutoffs) != 0 {
		t.Error("swept a project despite failing to read the override list")
	}
}

// A canceled context stops the pass rather than working through every project.
func TestSweepProjectOverridesStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &fakeStore{
		overrides:     map[int64]int{1: 1},
		projectRemain: map[int64]int64{1: 5000},
	}
	sweepProjectOverrides(ctx, store, Config{RetentionDays: 30}, fastTuning(), quietLogger(), nil)

	if store.projectDeletes[1] != 0 {
		t.Error("kept deleting after the context was canceled")
	}
}
