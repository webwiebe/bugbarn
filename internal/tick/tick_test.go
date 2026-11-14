package tick

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStartCallsFnPerIntervalAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	var wg sync.WaitGroup
	Start(ctx, &wg, time.Millisecond, func(now time.Time) {
		select {
		case ticks <- now:
		case <-ctx.Done():
		}
	})
	for i := 0; i < 3; i++ {
		select {
		case now := <-ticks:
			if now.IsZero() {
				t.Fatal("fn got a zero time")
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("tick %d never came", i)
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("loop kept running after cancel")
	}
}

func TestRunReturnsWithoutCallingFnWhenAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Run(ctx, time.Hour, func(time.Time) { t.Error("fn called on a canceled context") })
}
