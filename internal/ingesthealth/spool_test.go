package ingesthealth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// spoolFixture is a writer spool the test changes between samples. The queue
// depth stays 0 throughout: in queue mode the Redis queue does not pass through
// the spool worker, so a stalled worker leaves it empty (#95).
type spoolFixture struct {
	bytes       int64
	lastAdvance time.Time
}

func spoolDeps(lastEvent time.Time, sp *spoolFixture) Deps {
	return Deps{
		LastEventAt: func(context.Context) (time.Time, error) { return lastEvent, nil },
		QueueDepth:  func(context.Context) (int64, error) { return 0, nil },
		SpoolBacklog: func(context.Context) (SpoolBacklog, error) {
			return SpoolBacklog{Bytes: sp.bytes, LastAdvanceAt: sp.lastAdvance}, nil
		},
	}
}

func newSpoolMonitor(t *testing.T, sp *spoolFixture, start time.Time) (*Monitor, *time.Time, *bytes.Buffer, *fakeNotifier) {
	t.Helper()
	var logs bytes.Buffer
	f := &fakeNotifier{name: "fake"}
	// A recent last event: the Redis consumer keeps persisting, which is what
	// hid the stalled spool worker from the last-event rule. StaleAfter is the
	// 30 days testing uses for its idle periods; the spool check keeps its own
	// 30-minute default.
	m := New(Config{StaleAfter: 30 * 24 * time.Hour, Environment: "staging"},
		spoolDeps(start.Add(-10*time.Second), sp),
		slog.New(slog.NewJSONHandler(&logs, nil)))
	cur := start
	m.now = func() time.Time { return cur }
	m.AddNotifier(f)
	return m, &cur, &logs, f
}

// TestStalledSpoolWorkerAlertsAfterConfirmation: records wait in the spool and
// the cursor does not move. Once the stall passes 30 minutes it takes three
// consecutive samples to flip, and the alert carries the spool values.
func TestStalledSpoolWorkerAlertsAfterConfirmation(t *testing.T) {
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	sp := &spoolFixture{bytes: 0, lastAdvance: start.Add(-time.Hour)}
	m, cur, logs, f := newSpoolMonitor(t, sp, start)

	m.sample(context.Background()) // empty spool: the monitor sees it clear
	if s := m.Snapshot(); !s.Healthy || !s.SpoolBacklogKnown || s.SpoolStalledSeconds != 0 {
		t.Fatalf("empty spool must be healthy with no stall: %+v", s)
	}

	// Records arrive and the worker stops advancing. 33 samples bring the
	// stall past 30 minutes; the first two of those wait for confirmation.
	sp.bytes = 48_000
	for i := 1; i <= 32; i++ {
		*cur = start.Add(time.Duration(i) * time.Minute)
		m.sample(context.Background())
		if !m.Snapshot().Healthy {
			t.Fatalf("minute %d: unhealthy before the stall passed the threshold and was confirmed", i)
		}
	}
	*cur = start.Add(33 * time.Minute)
	m.sample(context.Background())
	s := m.Snapshot()
	if s.Healthy || s.UnhealthySamples != 3 {
		t.Fatalf("expected a confirmed spool stall on the third sample past the threshold: %+v", s)
	}
	if len(s.Reasons) != 1 || !strings.Contains(s.Reasons[0], "spool worker has not advanced for 33m0s with 48000 bytes unprocessed") {
		t.Fatalf("unexpected reasons: %v", s.Reasons)
	}
	if s.QueueDepth != 0 {
		t.Fatalf("the queue must stay empty in this scenario, got %d", s.QueueDepth)
	}
	if f.count() != 1 {
		t.Fatalf("expected one out-of-band alert, got %d", f.count())
	}
	out := logs.String()
	for _, want := range []string{
		`"msg":"ingest pipeline unhealthy"`,
		`"spool_backlog_bytes":48000`,
		`"spool_stalled_seconds":1980`,
		`"spool_backlog_known":true`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ERROR log missing %s:\n%s", want, out)
		}
	}
}

// TestIdleSpoolIsHealthy: an instance with nothing in the spool is healthy
// however long ago the worker last moved its cursor.
func TestIdleSpoolIsHealthy(t *testing.T) {
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	sp := &spoolFixture{bytes: 0, lastAdvance: start.Add(-72 * time.Hour)}
	m, cur, logs, f := newSpoolMonitor(t, sp, start)

	for i := 0; i < 10; i++ {
		*cur = start.Add(time.Duration(i) * time.Minute)
		m.sample(context.Background())
		if s := m.Snapshot(); !s.Healthy || s.UnhealthySamples != 0 || s.SpoolStalledSeconds != 0 {
			t.Fatalf("sample %d: idle spool must be healthy: %+v", i, s)
		}
	}
	if f.count() != 0 || strings.Contains(logs.String(), "unhealthy") {
		t.Fatalf("expected no alert and no warning, got:\n%s", logs.String())
	}
}

// TestSlowButAdvancingSpoolWorkerIsHealthy: the backlog never empties, but the
// worker keeps moving its cursor, so the stall time stays short.
func TestSlowButAdvancingSpoolWorkerIsHealthy(t *testing.T) {
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	sp := &spoolFixture{bytes: 900_000, lastAdvance: start.Add(-2 * time.Hour)}
	m, cur, _, f := newSpoolMonitor(t, sp, start)

	for i := 0; i < 90; i++ {
		*cur = start.Add(time.Duration(i) * time.Minute)
		sp.lastAdvance = cur.Add(-20 * time.Second)
		sp.bytes -= 5_000
		m.sample(context.Background())
		s := m.Snapshot()
		if !s.Healthy || s.UnhealthySamples != 0 {
			t.Fatalf("minute %d: a draining worker must stay healthy: %+v", i, s)
		}
		if i > 0 && s.SpoolStalledSeconds > 60 {
			t.Fatalf("minute %d: stall should track the cursor, got %.0fs", i, s.SpoolStalledSeconds)
		}
	}
	if f.count() != 0 {
		t.Fatalf("expected no alert, got %d", f.count())
	}
}

// TestSpoolRolloutBlipDoesNotAlert covers a writer restart. A record was
// appended just before the writer stopped, the cursor is hours old because the
// instance had been idle, and the monitor (a reader replaced in the same
// rollout) has never seen the spool empty. It counts from its own first
// sample, so the minutes the new writer takes to start do not alert.
func TestSpoolRolloutBlipDoesNotAlert(t *testing.T) {
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	sp := &spoolFixture{bytes: 700, lastAdvance: start.Add(-5 * time.Hour)}
	m, cur, logs, f := newSpoolMonitor(t, sp, start)

	for i := 0; i <= 10; i++ {
		*cur = start.Add(time.Duration(i) * time.Minute)
		m.sample(context.Background())
		if s := m.Snapshot(); !s.Healthy || s.UnhealthySamples != 0 {
			t.Fatalf("minute %d: writer still starting, must not count as a stall: %+v", i, s)
		}
	}
	// The writer is up and drains the record.
	sp.bytes = 0
	sp.lastAdvance = start.Add(11 * time.Minute)
	*cur = start.Add(11 * time.Minute)
	m.sample(context.Background())
	if s := m.Snapshot(); !s.Healthy || s.SpoolStalledSeconds != 0 {
		t.Fatalf("drained spool must be healthy: %+v", s)
	}
	if f.count() != 0 || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("expected no alert, got:\n%s", logs.String())
	}
}

// TestSpoolBacklogAfterIdleCountsFromLastEmptySample: after a day of idling
// the cursor is a day old. A record that arrives must count from the last
// sample that saw the spool empty, so one slow sample is not a day of stall.
func TestSpoolBacklogAfterIdleCountsFromLastEmptySample(t *testing.T) {
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	sp := &spoolFixture{bytes: 0, lastAdvance: start.Add(-24 * time.Hour)}
	m, cur, _, _ := newSpoolMonitor(t, sp, start)

	m.sample(context.Background())
	sp.bytes = 300
	*cur = start.Add(time.Minute)
	m.sample(context.Background())
	if s := m.Snapshot(); s.SpoolStalledSeconds != 60 {
		t.Fatalf("stall should count from the last empty sample, got %.0fs", s.SpoolStalledSeconds)
	}
}

// TestSpoolCheckSkippedWithoutSource: no spool access leaves the fields unset.
func TestSpoolCheckSkippedWithoutSource(t *testing.T) {
	m := New(Config{}, Deps{}, nil)
	m.sample(context.Background())
	if s := m.Snapshot(); s.SpoolBacklogKnown || !s.Healthy {
		t.Fatalf("expected the spool check to be skipped: %+v", s)
	}
}
