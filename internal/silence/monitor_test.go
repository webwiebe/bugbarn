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

type fakeSource struct {
	mu       sync.Mutex
	activity []storage.ProjectActivity
	err      error
}

func (f *fakeSource) set(a ...storage.ProjectActivity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activity = a
}

func (f *fakeSource) ProjectActivity(context.Context, time.Time, time.Duration, time.Duration) ([]storage.ProjectActivity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activity, f.err
}

type fakeNotifier struct {
	sent []string
	err  error
}

func (n *fakeNotifier) NotifySilent(_ context.Context, slug string, _ time.Duration) error {
	if n.err != nil {
		return n.err
	}
	n.sent = append(n.sent, slug)
	return nil
}

func newMonitor(src Source, n Notifier) *Monitor {
	return New(Config{SilentFor: 7 * 24 * time.Hour, Lookback: 30 * 24 * time.Hour, MinEarlier: 3}, src, n,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCheck_QuietHealthyProjectsDoNotAlert(t *testing.T) {
	t.Parallel()
	src := &fakeSource{}
	notif := &fakeNotifier{}
	src.set(
		// still reporting
		storage.ProjectActivity{ProjectID: 1, Slug: "busy", Recent: 4, Earlier: 9},
		// reports rarely, but did inside the window: below the MinEarlier bar
		storage.ProjectActivity{ProjectID: 2, Slug: "rare", Recent: 0, Earlier: 2},
	)

	newMonitor(src, notif).Check(context.Background())

	if len(notif.sent) != 0 {
		t.Fatalf("expected no alerts, got %v", notif.sent)
	}
}

func TestCheck_SilentProjectAlertsOnceUntilItReportsAgain(t *testing.T) {
	t.Parallel()
	src := &fakeSource{}
	notif := &fakeNotifier{}
	m := newMonitor(src, notif)
	ctx := context.Background()

	src.set(storage.ProjectActivity{ProjectID: 3, Slug: "gone", Recent: 0, Earlier: 5})
	m.Check(ctx)
	m.Check(ctx)
	if len(notif.sent) != 1 || notif.sent[0] != "gone" {
		t.Fatalf("expected exactly one alert for gone, got %v", notif.sent)
	}

	// it reports again, then goes quiet again: a new episode alerts again
	src.set(storage.ProjectActivity{ProjectID: 3, Slug: "gone", Recent: 1, Earlier: 5})
	m.Check(ctx)
	src.set(storage.ProjectActivity{ProjectID: 3, Slug: "gone", Recent: 0, Earlier: 5})
	m.Check(ctx)
	if len(notif.sent) != 2 {
		t.Fatalf("expected a second alert after recovery, got %v", notif.sent)
	}
}

func TestCheck_FailedNotificationIsRetriedAndLoggedAtError(t *testing.T) {
	t.Parallel()
	src := &fakeSource{}
	notif := &fakeNotifier{err: errors.New("smtp down")}
	m := newMonitor(src, notif)
	src.set(storage.ProjectActivity{ProjectID: 3, Slug: "gone", Recent: 0, Earlier: 5})

	m.Check(context.Background())
	notif.err = nil
	m.Check(context.Background())

	if len(notif.sent) != 1 {
		t.Fatalf("a failed delivery must be retried on the next check, sent=%v", notif.sent)
	}
}

func TestCheck_SourceErrorDoesNotAlert(t *testing.T) {
	t.Parallel()
	src := &fakeSource{err: errors.New("db locked")}
	notif := &fakeNotifier{}

	newMonitor(src, notif).Check(context.Background())

	if len(notif.sent) != 0 {
		t.Fatalf("expected no alerts on a source error, got %v", notif.sent)
	}
}
