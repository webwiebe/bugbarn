package silence

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestDefaultConfigValues(t *testing.T) {
	t.Parallel()
	c := DefaultConfig()
	if c.SilentFor != 7*24*time.Hour || c.Lookback != 30*24*time.Hour || c.MinEarlier != 3 || c.Interval != time.Hour {
		t.Fatalf("DefaultConfig = %+v", c)
	}
}

// stopAndWait cancels ctx and waits for the loop's WaitGroup.
func stopAndWait(t *testing.T, cancel context.CancelFunc, wg *sync.WaitGroup) {
	t.Helper()
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
}

// signalSource answers ProjectActivity and tells the test each time it was asked.
type signalSource struct {
	calls chan struct{}
}

func (s signalSource) ProjectActivity(context.Context, time.Time, time.Duration, time.Duration) ([]storage.ProjectActivity, error) {
	select {
	case s.calls <- struct{}{}:
	default:
	}
	return nil, nil
}

func TestMonitorStartChecksOnEveryTickUntilCanceled(t *testing.T) {
	t.Parallel()
	src := signalSource{calls: make(chan struct{}, 8)}
	m := New(Config{Interval: 2 * time.Millisecond}, src, &fakeNotifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	m.Start(ctx, &wg)
	for i := 0; i < 2; i++ {
		select {
		case <-src.calls:
		case <-time.After(30 * time.Second):
			t.Fatal("no check within 30s")
		}
	}
	stopAndWait(t, cancel, &wg)
}

func TestMonitorStartDefaultsAZeroIntervalToAnHour(t *testing.T) {
	t.Parallel()
	m := New(Config{}, signalSource{calls: make(chan struct{}, 1)}, &fakeNotifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	m.Start(ctx, &wg)
	stopAndWait(t, cancel, &wg)
}

// countingShipper reports each sweep on a channel.
type countingShipper struct {
	recordingShipper
	sweeps chan [2]int
}

func (s *countingShipper) ShipSweep(_ context.Context, _ Mode, projects, wouldFire int) {
	select {
	case s.sweeps <- [2]int{projects, wouldFire}:
	default:
	}
}

func TestRulesStartSweepsOnEveryTickUntilCanceled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.project("young")
	shipper := &countingShipper{sweeps: make(chan [2]int, 8)}
	cfg := DefaultRulesConfig()
	cfg.Mode = ModeDryRun
	cfg.Interval = 2 * time.Millisecond
	r := NewRules(cfg, StoreSource{ProjectStore: f.store.Domains().Projects}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithShipper(shipper)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Start(ctx, &wg)
	select {
	case got := <-shipper.sweeps:
		if got[0] < 1 || got[1] != 0 {
			t.Fatalf("sweep counts = %v", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no sweep within 30s")
	}
	stopAndWait(t, cancel, &wg)
}

func TestRulesStartInOffModeStartsNothing(t *testing.T) {
	t.Parallel()
	cfg := DefaultRulesConfig()
	cfg.Mode = ModeOff
	r := NewRules(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var wg sync.WaitGroup
	r.Start(context.Background(), &wg)
	wg.Wait() // returns at once: no goroutine was added
}

func TestRulesStartDefaultsAZeroIntervalToAnHour(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := DefaultRulesConfig()
	cfg.Mode = ModeDryRun
	cfg.Interval = 0
	r := NewRules(cfg, StoreSource{ProjectStore: f.store.Domains().Projects}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	r.Start(ctx, &wg)
	stopAndWait(t, cancel, &wg)
}

type failingLogs struct{}

func (failingLogs) InsertLogEntries(context.Context, []storage.LogEntry) error {
	return errors.New("disk full")
}

func TestShipSweepWarnsWhenTheProjectOrTheInsertFails(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	missing := NewLogShipper(&fakeLogs{}, &fakeIssues{targetEr: errors.New("no project")}, "bugbarn-service", log)
	missing.ShipSweep(context.Background(), ModeDryRun, 1, 0)

	failing := NewLogShipper(failingLogs{}, &fakeIssues{target: storage.Project{ID: 5}}, "bugbarn-service", log)
	failing.ShipSweep(context.Background(), ModeDryRun, 1, 0)
	failing.Ship(context.Background(), Finding{Slug: "a", Rule: RuleLogs}, ModeDryRun)
	missing.Ship(context.Background(), Finding{Slug: "a", Rule: RuleLogs}, ModeDryRun)
}
