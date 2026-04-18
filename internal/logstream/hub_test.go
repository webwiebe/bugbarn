package logstream

import (
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestSubscribeAndPublish(t *testing.T) {
	t.Parallel()
	hub := NewHub()

	ch, cancel := hub.Subscribe(1)
	defer cancel()

	entry := storage.LogEntry{
		ID:         1,
		ProjectID:  1,
		ReceivedAt: time.Now().UTC(),
		LevelNum:   30,
		Level:      "info",
		Message:    "test message",
	}

	hub.Publish(1, entry)

	select {
	case got := <-ch:
		if got.Message != entry.Message {
			t.Errorf("unexpected message: %q", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published entry")
	}
}

func TestPublishToOtherProjectDoesNotDeliver(t *testing.T) {
	t.Parallel()
	hub := NewHub()

	ch, cancel := hub.Subscribe(1)
	defer cancel()

	entry := storage.LogEntry{ProjectID: 2, Message: "for project 2"}
	hub.Publish(2, entry)

	select {
	case got := <-ch:
		t.Errorf("unexpected delivery to project 1: %v", got)
	case <-time.After(50 * time.Millisecond):
		// expected: nothing delivered
	}
}

func TestCancelRemovesSubscriber(t *testing.T) {
	t.Parallel()
	hub := NewHub()

	_, cancel := hub.Subscribe(1)
	cancel() // unsubscribe

	hub.mu.RLock()
	remaining := len(hub.subs[1])
	hub.mu.RUnlock()

	if remaining != 0 {
		t.Errorf("expected 0 subscribers after cancel, got %d", remaining)
	}
}

func TestNonBlockingPublishDropsOnSlowConsumer(t *testing.T) {
	t.Parallel()
	hub := NewHub()

	ch, cancel := hub.Subscribe(1)
	defer cancel()

	// Fill the buffer (size 64) without reading.
	entry := storage.LogEntry{ProjectID: 1, Message: "fill"}
	for i := 0; i < 128; i++ {
		hub.Publish(1, entry) // must not block or panic
	}

	// Channel should be full but not deadlocked; drain what's there.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			goto done
		}
	}
done:
	if drained == 0 {
		t.Error("expected some entries in channel")
	}
	if drained > 64 {
		t.Errorf("expected at most 64 entries (buffer size), got %d", drained)
	}
}
