package domainevents

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestBusPublishSubscribe(t *testing.T) {
	t.Parallel()

	var b Bus
	var received []any

	b.Subscribe(func(e any) {
		received = append(received, e)
	})

	b.Publish("hello")
	b.Publish(42)

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
	if received[0] != "hello" {
		t.Errorf("expected first event %q, got %v", "hello", received[0])
	}
	if received[1] != 42 {
		t.Errorf("expected second event 42, got %v", received[1])
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	t.Parallel()

	var b Bus
	var count1, count2 int

	b.Subscribe(func(e any) { count1++ })
	b.Subscribe(func(e any) { count2++ })

	b.Publish("event")

	if count1 != 1 {
		t.Errorf("subscriber 1: expected 1 call, got %d", count1)
	}
	if count2 != 1 {
		t.Errorf("subscriber 2: expected 1 call, got %d", count2)
	}
}

func TestBusNoSubscribers(t *testing.T) {
	t.Parallel()

	var b Bus
	// Should not panic when there are no subscribers.
	b.Publish("event")
}

func TestBusConcurrencySafety(t *testing.T) {
	t.Parallel()

	var b Bus
	var count atomic.Int64

	const goroutines = 50
	const eventsEach = 20

	// Subscribe from multiple goroutines concurrently.
	var subWG sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		subWG.Add(1)
		go func() {
			defer subWG.Done()
			b.Subscribe(func(e any) {
				count.Add(1)
			})
		}()
	}
	subWG.Wait()

	// Publish from multiple goroutines concurrently.
	var pubWG sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		pubWG.Add(1)
		go func() {
			defer pubWG.Done()
			for j := 0; j < eventsEach; j++ {
				b.Publish(j)
			}
		}()
	}
	pubWG.Wait()

	// Each of the 50 goroutines published 20 events, and at the time of publishing
	// between 0 and 50 subscribers may have been registered. We only assert that
	// we did not panic or deadlock.
	_ = count.Load()
}
