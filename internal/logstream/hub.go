package logstream

import (
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Hub fans published log entries out to registered SSE subscribers.
type Hub struct {
	mu   sync.RWMutex
	subs map[int64][]chan storage.LogEntry // projectID → channels
}

// NewHub creates a new Hub ready for use.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[int64][]chan storage.LogEntry),
	}
}

// Subscribe returns a channel that receives entries for projectID, and a cancel func.
// Buffer size 64; slow consumers miss entries (non-blocking send).
func (h *Hub) Subscribe(projectID int64) (<-chan storage.LogEntry, func()) {
	ch := make(chan storage.LogEntry, 64)

	h.mu.Lock()
	h.subs[projectID] = append(h.subs[projectID], ch)
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		chans := h.subs[projectID]
		for i, c := range chans {
			if c == ch {
				h.subs[projectID] = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		if len(h.subs[projectID]) == 0 {
			delete(h.subs, projectID)
		}
		close(ch)
	}

	return ch, cancel
}

// Publish sends an entry to all current subscribers for that project,
// and also to all-projects subscribers (those subscribed with projectID=0).
// Uses non-blocking sends so slow consumers do not block the publisher.
func (h *Hub) Publish(projectID int64, entry storage.LogEntry) {
	h.mu.RLock()
	chans := h.subs[projectID]
	var allChans []chan storage.LogEntry
	if projectID != 0 {
		allChans = h.subs[0]
	}
	h.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- entry:
		default:
		}
	}
	for _, ch := range allChans {
		select {
		case ch <- entry:
		default:
		}
	}
}
