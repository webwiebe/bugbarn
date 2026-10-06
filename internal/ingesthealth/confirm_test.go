package ingesthealth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// rolloutDeps returns deps whose queue depth the test sets between samples.
func rolloutDeps(lastEvent time.Time, depth *int64) Deps {
	return Deps{
		LastEventAt: func(context.Context) (time.Time, error) { return lastEvent, nil },
		QueueDepth:  func(context.Context) (int64, error) { return *depth, nil },
	}
}

// TestRolloutQueueBlipDoesNotAlert reproduces BS2-107. Production had gone 45
// minutes without an error event, which is normal on a quiet evening. During a
// rollout the writer restarts while the readers keep queueing logs, security
// and metrics batches, so a new pod's first sample saw a non-empty queue. That
// one sample logged "ingest pipeline unhealthy" although the queue drained
// seconds later. A single sample must not alert.
func TestRolloutQueueBlipDoesNotAlert(t *testing.T) {
	now := time.Date(2026, 10, 6, 18, 0, 23, 0, time.UTC)
	depth := int64(4)
	var logs bytes.Buffer
	f := &fakeNotifier{name: "fake"}
	m := New(Config{StaleAfter: 30 * time.Minute, Environment: "production"},
		rolloutDeps(now.Add(-45*time.Minute), &depth),
		slog.New(slog.NewJSONHandler(&logs, nil)))
	cur := now
	m.now = func() time.Time { return cur }
	m.AddNotifier(f)

	m.sample(context.Background())
	s := m.Snapshot()
	if !s.Healthy || len(s.Reasons) != 0 {
		t.Fatalf("one unhealthy sample must not flip health: %+v", s)
	}
	if s.UnhealthySamples != 1 {
		t.Fatalf("expected the suspect sample to be counted, got %d", s.UnhealthySamples)
	}

	// The writer is back and has drained the queue by the next sample.
	depth = 0
	cur = now.Add(time.Minute)
	m.sample(context.Background())
	if s := m.Snapshot(); !s.Healthy || s.UnhealthySamples != 0 {
		t.Fatalf("a drained queue must reset the streak: %+v", s)
	}

	if f.count() != 0 {
		t.Fatalf("expected no out-of-band alert, got %d", f.count())
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("expected no ERROR log (ERROR is self-reported), got:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "waiting for confirmation") {
		t.Fatalf("expected the suspect sample at WARN, got:\n%s", logs.String())
	}
}

// TestInterruptedStreakDoesNotAlert: the unhealthy samples must be consecutive.
func TestInterruptedStreakDoesNotAlert(t *testing.T) {
	now := time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC)
	depth := int64(0)
	m := New(Config{StaleAfter: 30 * time.Minute}, rolloutDeps(now.Add(-time.Hour), &depth), nil)
	cur := now
	m.now = func() time.Time { return cur }

	for i, d := range []int64{3, 5, 0, 2, 7} {
		depth = d
		cur = now.Add(time.Duration(i) * time.Minute)
		m.sample(context.Background())
		if s := m.Snapshot(); !s.Healthy {
			t.Fatalf("sample %d (depth %d): no three consecutive unhealthy samples yet, got %v", i, d, s.Reasons)
		}
	}
}

// TestSustainedStallAlertsAfterConfirmation keeps the protection the monitor
// exists for: a queue that stays non-empty while nothing is persisted flips
// unhealthy on the third sample, and the ERROR log names the environment and
// the reason so the self-reported event can be triaged.
func TestSustainedStallAlertsAfterConfirmation(t *testing.T) {
	now := time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC)
	depth := int64(120)
	var logs bytes.Buffer
	f := &fakeNotifier{name: "fake"}
	m := New(Config{StaleAfter: 30 * time.Minute, Environment: "production"},
		rolloutDeps(now.Add(-2*time.Hour), &depth),
		slog.New(slog.NewJSONHandler(&logs, nil)))
	cur := now
	m.now = func() time.Time { return cur }
	m.AddNotifier(f)

	for i := 0; i < 2; i++ {
		cur = now.Add(time.Duration(i) * time.Minute)
		m.sample(context.Background())
		if !m.Snapshot().Healthy {
			t.Fatalf("sample %d: must not flip before confirmation", i)
		}
	}
	cur = now.Add(2 * time.Minute)
	m.sample(context.Background())
	s := m.Snapshot()
	if s.Healthy || len(s.Reasons) == 0 || s.UnhealthySamples != 3 {
		t.Fatalf("expected a confirmed stall on the third sample: %+v", s)
	}
	if f.count() != 1 {
		t.Fatalf("expected one out-of-band alert, got %d", f.count())
	}
	out := logs.String()
	for _, want := range []string{
		`"msg":"ingest pipeline unhealthy"`,
		`"environment":"production"`,
		`events queued but not persisted`,
		`"unhealthy_samples":3`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %s in the alert log, got:\n%s", want, out)
		}
	}
}
