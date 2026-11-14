package monitorwatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestDefaultConfigValues(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	if c.Interval != 30*time.Second || c.StartupGrace != 5*time.Minute {
		t.Fatalf("DefaultConfig = %+v", c)
	}
}

// fakeStore returns one monitor and records the marks the detector makes.
type fakeStore struct {
	mu        sync.Mutex
	monitors  []domain.Monitor
	listErr   error
	missedErr error
	applied   bool
	lists     chan struct{}
	missed    int
	emitted   int
}

func (f *fakeStore) ListEnabledMonitors(context.Context) ([]domain.Monitor, error) {
	if f.lists != nil {
		select {
		case f.lists <- struct{}{}:
		default:
		}
	}
	return f.monitors, f.listErr
}

func (f *fakeStore) MarkMonitorMissed(context.Context, int64, time.Time, time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missed++
	return f.applied, f.missedErr
}

func (f *fakeStore) MarkMonitorEmitted(context.Context, int64, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted++
	return nil
}

type fakeReporter struct{ reports int }

func (r *fakeReporter) Missed(context.Context, domain.Monitor) error { r.reports++; return nil }

func overdueMonitor(now time.Time) domain.Monitor {
	return domain.Monitor{
		ID: 1, Slug: "sweep", State: domain.MonitorStateOK,
		LastCheckInAt: now.Add(-24 * time.Hour), Interval: time.Minute,
	}
}

func TestStartScansOnEveryTickUntilCanceled(t *testing.T) {
	t.Parallel()
	store := &fakeStore{lists: make(chan struct{}, 8)}
	d := NewDetector(Config{Interval: 2 * time.Millisecond}, store, &fakeReporter{}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	d.Start(ctx, &wg)
	for i := 0; i < 2; i++ {
		select {
		case <-store.lists:
		case <-time.After(30 * time.Second):
			t.Fatal("no scan within 30s")
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("detector did not stop after cancel")
	}
}

func TestCheckLogsAListErrorAndStops(t *testing.T) {
	t.Parallel()
	store := &fakeStore{listErr: errors.New("db gone")}
	rep := &fakeReporter{}
	NewDetector(Config{}, store, rep, quiet).Check(context.Background())
	if store.missed != 0 || rep.reports != 0 {
		t.Fatalf("a failed list marked %d and reported %d", store.missed, rep.reports)
	}
}

func TestMarkMissedStopsOnStoreErrorAndOnLostRace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		applied bool
		err     error
	}{
		"store error":     {applied: true, err: errors.New("locked")},
		"check-in landed": {applied: false},
	} {
		store := &fakeStore{monitors: []domain.Monitor{overdueMonitor(now)}, applied: tc.applied, missedErr: tc.err}
		rep := &fakeReporter{}
		d := NewDetector(Config{StartupGrace: 0}, store, rep, quiet)
		d.SetClock(func() time.Time { return now })
		d.Check(context.Background())
		if store.missed != 1 {
			t.Fatalf("%s: MarkMonitorMissed called %d times, want 1", name, store.missed)
		}
		if rep.reports != 0 || store.emitted != 0 {
			t.Fatalf("%s: reported %d, emitted %d, want none", name, rep.reports, store.emitted)
		}
	}
}

func TestMarkMissedReportsWhenApplied(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{monitors: []domain.Monitor{overdueMonitor(now)}, applied: true}
	rep := &fakeReporter{}
	d := NewDetector(Config{StartupGrace: 0}, store, rep, quiet)
	d.SetClock(func() time.Time { return now })
	d.Check(context.Background())
	if rep.reports != 1 || store.emitted != 1 {
		t.Fatalf("reported %d, emitted %d, want 1 each", rep.reports, store.emitted)
	}
}
