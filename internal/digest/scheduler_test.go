package digest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/mail"
)

var base = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)

func baseConfig() Config {
	return Config{Day: int(base.Weekday()), Hour: base.Hour(), WebhookURL: "http://127.0.0.1:1/hook"}
}

func TestDue(t *testing.T) {
	cfg := baseConfig()
	cases := []struct {
		name string
		now  time.Time
		last time.Time
		want bool
	}{
		{"matching slot, never fired", base.Add(30 * time.Minute), time.Time{}, true},
		{"wrong hour", base.Add(time.Hour), time.Time{}, false},
		{"wrong weekday", base.AddDate(0, 0, 1), time.Time{}, false},
		{"fired 10 minutes ago", base.Add(10 * time.Minute), base, false},
		{"fired 59 minutes ago in the same slot", base.Add(59 * time.Minute), base, false},
		{"next week", base.AddDate(0, 0, 7), base, true},
		{"non-UTC clock is compared in UTC", base.In(time.FixedZone("CET", 3600)), time.Time{}, true},
	}
	for _, tc := range cases {
		if got := due(cfg, tc.now, tc.last); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// driveTicks feeds the ticks to runTicks one at a time and returns once the last
// one has been handled, then stops the loop.
func driveTicks(t *testing.T, cfg Config, store Store, notifiers []Notifier, ticks ...time.Time) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTicks(ctx, cfg, store, notifiers, ch)
	}()
	for _, tick := range ticks {
		ch <- tick
	}
	// The loop only reads this tick after it finished handling the previous one.
	ch <- time.Time{}
	cancel()
	<-done
}

func TestRunTicksFiresOncePerSlot(t *testing.T) {
	store := &fakeStore{projects: makeProjects(1)}
	notifier := &fakeNotifier{}
	driveTicks(t, baseConfig(), store, []Notifier{notifier},
		base.Add(-time.Minute),  // before the slot
		base,                    // fires
		base.Add(time.Minute),   // same hour, already fired
		base.Add(2*time.Minute), // same hour, already fired
		base.AddDate(0, 0, 7),   // next week fires again
		base.AddDate(0, 0, 7).Add(time.Minute),
	)
	if got := len(notifier.delivered()); got != 2 {
		t.Fatalf("delivered %d digests, want 2 (one per weekly slot)", got)
	}
}

func TestRunTicksStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runTicks(ctx, baseConfig(), &fakeStore{}, nil, make(chan time.Time))
		close(done)
	}()
	<-done
}

func TestStartSchedulerNoOps(t *testing.T) {
	var wg sync.WaitGroup
	// Disabled config: nothing starts and the WaitGroup stays at zero.
	StartScheduler(context.Background(), Config{}, &fakeStore{}, &wg)
	wg.Wait()

	// Mail active but webhook empty still builds a notifier; a config that
	// enables nothing builds none.
	if n := BuildNotifiers(Config{}); len(n) != 0 {
		t.Fatalf("notifiers = %d, want 0", len(n))
	}
}

func TestStartSchedulerStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	StartScheduler(ctx, baseConfig(), &fakeStore{}, &wg)
	cancel()
	wg.Wait()
	// A nil WaitGroup is allowed.
	ctx2, cancel2 := context.WithCancel(context.Background())
	StartScheduler(ctx2, baseConfig(), &fakeStore{}, nil)
	cancel2()
}

func TestBuildNotifiers(t *testing.T) {
	cfg := Config{
		WebhookURL: "https://hooks.example/x",
		Mail:       mail.Config{Enabled: true, Host: "smtp.example.com", To: "ops@example.com"},
	}
	got := BuildNotifiers(cfg)
	if len(got) != 2 || got[0].Name() != "webhook" || got[1].Name() != "email" {
		t.Fatalf("notifiers = %v", got)
	}
	if got := BuildNotifiers(Config{WebhookURL: "https://hooks.example/x"}); len(got) != 1 || got[0].Name() != "webhook" {
		t.Fatalf("webhook only = %v", got)
	}
}
