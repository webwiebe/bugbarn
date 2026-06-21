package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestLastEventReceivedAt(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// No events yet: zero time, no error (so the monitor treats it as "no data"
	// rather than a stall).
	last, err := store.LastEventReceivedAt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !last.IsZero() {
		t.Fatalf("expected zero time with no events, got %v", last)
	}

	older := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 21, 11, 30, 0, 0, time.UTC)
	for _, ts := range []time.Time{older, newer} {
		evt := processedEventFrom(event.Event{
			ObservedAt: ts,
			ReceivedAt: ts,
			Severity:   "ERROR",
			Message:    "boom",
			Exception:  event.Exception{Type: "Error", Message: "boom"},
		})
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, evt); err != nil {
			t.Fatal(err)
		}
	}

	last, err = store.LastEventReceivedAt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !last.Equal(newer) {
		t.Fatalf("expected most recent received_at %v, got %v", newer, last)
	}
}
