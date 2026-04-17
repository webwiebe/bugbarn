package domainevents

import "sync"

// Handler is a function that receives a domain event.
type Handler func(event any)

// Bus is a simple synchronous in-process pub/sub bus.
type Bus struct {
	mu       sync.RWMutex
	handlers []Handler
}

// Subscribe registers a handler to receive all future published events.
func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	b.handlers = append(b.handlers, h)
	b.mu.Unlock()
}

// Publish delivers an event synchronously to all registered handlers.
func (b *Bus) Publish(event any) {
	b.mu.RLock()
	hs := append([]Handler(nil), b.handlers...)
	b.mu.RUnlock()
	for _, h := range hs {
		h(event)
	}
}
