package digest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

// A failed delivery is logged at ERROR once, in Send. The scheduler reports the
// outcome without a second ERROR record for the same failure.
func TestDeliveryFailureLogsOneErrorThroughTheInjectedLogger(t *testing.T) {
	log, rec := logtest.New()
	store := &fakeStore{projects: makeProjects(1)}
	bad := &fakeNotifier{nameText: "webhook", sendErr: errors.New("boom")}

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTicks(ctx, log, baseConfig(), store, []Notifier{bad}, ch)
	}()
	ch <- base
	ch <- time.Time{} // read only after the first tick was handled
	cancel()
	<-done

	if n := len(rec.Errors()); n != 1 {
		t.Fatalf("ERROR records = %d, want 1", n)
	}
}

func TestSendWithHealthyNotifierLogsNoError(t *testing.T) {
	log, rec := logtest.New()
	store := &fakeStore{projects: makeProjects(2)}
	if errs := Send(context.Background(), log, Config{}, store, []Notifier{&fakeNotifier{}}); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if n := len(rec.Errors()); n != 0 {
		t.Fatalf("ERROR records = %d, want 0", n)
	}
}

func TestSendWithoutLoggerFallsBackToDefault(t *testing.T) {
	store := &fakeStore{projects: makeProjects(1)}
	if errs := Send(context.Background(), nil, Config{}, store, []Notifier{&fakeNotifier{}}); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
}
