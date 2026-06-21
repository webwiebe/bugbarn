package ingesthealth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func baseDeps(lastEvent time.Time, depth int64) Deps {
	return Deps{
		LastEventAt: func(context.Context) (time.Time, error) { return lastEvent, nil },
		QueueDepth:  func(context.Context) (int64, error) { return depth, nil },
	}
}

func TestSampleHealthyWhenRecent(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: 30 * time.Minute, MaxQueueDepth: 1000}, baseDeps(now.Add(-time.Minute), 10), nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	s := m.Snapshot()

	if !s.Healthy {
		t.Fatalf("expected healthy, got reasons %v", s.Reasons)
	}
	if !s.HasEvents || s.LastEventAgeSeconds != 60 {
		t.Fatalf("unexpected age: hasEvents=%v age=%v", s.HasEvents, s.LastEventAgeSeconds)
	}
	if !s.QueueDepthKnown || s.QueueDepth != 10 {
		t.Fatalf("unexpected queue depth: %+v", s)
	}
}

// TestSampleStalledIngest is the core regression for the silent outage: when no
// event has been persisted for longer than the threshold, the monitor must mark
// the pipeline unhealthy so the health probe can report it.
func TestSampleStalledIngest(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: 30 * time.Minute}, baseDeps(now.Add(-5*24*time.Hour), 0), nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	s := m.Snapshot()

	if s.Healthy {
		t.Fatal("expected unhealthy for 5-day-old last event")
	}
	if len(s.Reasons) == 0 {
		t.Fatal("expected a reason explaining the stall")
	}
}

func TestSampleBacklogOverThreshold(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: time.Hour, MaxQueueDepth: 50_000}, baseDeps(now, 411_000), nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	s := m.Snapshot()

	if s.Healthy {
		t.Fatal("expected unhealthy for backlog over threshold")
	}
	found := false
	for _, r := range s.Reasons {
		if len(r) > 0 && (r[:4] == "writ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a backlog reason, got %v", s.Reasons)
	}
}

func TestSampleNoEventsIsNotStalled(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: time.Minute}, baseDeps(time.Time{}, 0), nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	s := m.Snapshot()

	if !s.Healthy {
		t.Fatalf("a fresh deployment with no events must not be flagged unhealthy: %v", s.Reasons)
	}
	if s.HasEvents {
		t.Fatal("expected HasEvents=false")
	}
}

func TestSampleMeasuresWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bugbarn.db")
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{}, Deps{
		LastEventAt: func(context.Context) (time.Time, error) { return now, nil },
		DBPath:      dbPath,
	}, nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	if got := m.Snapshot().WALSizeBytes; got != 4096 {
		t.Fatalf("expected WAL size 4096, got %d", got)
	}
}

func TestQueueDepthErrorDoesNotMarkUnhealthy(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: time.Hour, MaxQueueDepth: 10}, Deps{
		LastEventAt: func(context.Context) (time.Time, error) { return now, nil },
		QueueDepth:  func(context.Context) (int64, error) { return 0, errors.New("redis down") },
	}, nil)
	m.now = func() time.Time { return now }

	m.sample(context.Background())
	s := m.Snapshot()
	if !s.Healthy {
		t.Fatalf("a transient queue-depth read error must not flip health: %v", s.Reasons)
	}
	if s.QueueDepthKnown {
		t.Fatal("expected QueueDepthKnown=false when the read errored")
	}
}

func TestAlertThrottled(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: time.Minute, AlertEvery: 15 * time.Minute}, baseDeps(now.Add(-time.Hour), 0), nil)
	cur := now
	m.now = func() time.Time { return cur }

	// First unhealthy sample alerts and records the time.
	m.sample(context.Background())
	first := m.lastAlert
	if first.IsZero() {
		t.Fatal("expected first unhealthy sample to record an alert time")
	}

	// A second sample within the throttle window must not re-alert.
	cur = now.Add(5 * time.Minute)
	m.sample(context.Background())
	if !m.lastAlert.Equal(first) {
		t.Fatal("alert should be throttled within AlertEvery")
	}

	// After the window, it alerts again.
	cur = now.Add(20 * time.Minute)
	m.sample(context.Background())
	if m.lastAlert.Equal(first) {
		t.Fatal("alert should fire again after the throttle window")
	}
}
