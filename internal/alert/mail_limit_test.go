package alert

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
)

func emailRule() Rule {
	return Rule{ID: "alert-000001", Name: "Mail", Condition: "new_issue", EmailTo: "ops@example.com", ProjectID: 1}
}

// waitFor polls cond until it holds. The conditions here are counters that the
// code under test advances, so the wait ends when the real transition happens.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A burst of alert emails must not open more SMTP connections than the limit,
// and what does not fit the queue is dropped and counted rather than piling up.
func TestDelivererBoundsEmailBurst(t *testing.T) {
	const burst, concurrent, queued = 20, 2, 3

	d := NewDeliverer(digest.MailConfig{Enabled: true, Host: "mail.example.com"})
	d.setMailLimits(concurrent, queued)

	release := make(chan struct{})
	var inFlight, maxInFlight, sent atomic.Int64
	d.sendEmail = func(ctx context.Context, _ digest.MailConfig, _, _, _, _ string) error {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		sent.Add(1)
		return nil
	}

	var wg sync.WaitGroup
	var droppedErrs atomic.Int64
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Fire(context.Background(), emailRule(), testIssue(), "https://bugbarn.example.com"); errors.Is(err, ErrEmailDropped) {
				droppedErrs.Add(1)
			}
		}()
	}

	waitFor(t, "the overflow to be dropped", func() bool { return d.EmailDropped() == burst-concurrent-queued })
	close(release)
	wg.Wait()

	if got := maxInFlight.Load(); got != concurrent {
		t.Fatalf("max concurrent sends = %d; want %d", got, concurrent)
	}
	if got := sent.Load(); got != concurrent+queued {
		t.Fatalf("sent = %d; want %d", got, concurrent+queued)
	}
	if got := droppedErrs.Load(); got != burst-concurrent-queued {
		t.Fatalf("Fire returned ErrEmailDropped %d times; want %d", got, burst-concurrent-queued)
	}
	if got := d.EmailDropped(); got != burst-concurrent-queued {
		t.Fatalf("EmailDropped = %d; want %d", got, burst-concurrent-queued)
	}
}

// A send that waits for a slot past its deadline is lost too, so it is counted.
func TestDelivererCountsEmailThatTimesOutWaiting(t *testing.T) {
	d := NewDeliverer(digest.MailConfig{Enabled: true, Host: "mail.example.com"})
	d.setMailLimits(1, 1)

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	d.sendEmail = func(context.Context, digest.MailConfig, string, string, string, string) error {
		started <- struct{}{}
		<-release
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- d.Fire(context.Background(), emailRule(), testIssue(), "https://bugbarn.example.com")
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { waiter <- d.Fire(ctx, emailRule(), testIssue(), "https://bugbarn.example.com") }()
	waitFor(t, "the second send to queue", func() bool { return d.emailWaiting.Load() == 1 })
	cancel()

	err := <-waiter
	if !errors.Is(err, ErrEmailDropped) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want ErrEmailDropped wrapping context.Canceled", err)
	}
	if got := d.EmailDropped(); got != 1 {
		t.Fatalf("EmailDropped = %d; want 1", got)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first send: %v", err)
	}
}
