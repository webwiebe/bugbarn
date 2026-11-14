// Package tick runs a function on a fixed interval until a context ends.
package tick

import (
	"context"
	"sync"
	"time"
)

// Start runs fn in its own goroutine, registered on wg, once per interval
// until ctx ends. The first call happens one interval after Start; fn gets the
// ticker's time. interval must be positive.
func Start(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, fn func(now time.Time)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		Run(ctx, interval, fn)
	}()
}

// Run is the blocking form of Start: it returns when ctx ends.
func Run(ctx context.Context, interval time.Duration, fn func(now time.Time)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			fn(now)
		}
	}
}
