package bugbarn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type transport struct {
	apiKey      string
	endpoint    string
	projectSlug string
	queue       chan envelope
	done        chan struct{}
	client      *http.Client
}

func newTransport(apiKey, endpoint, projectSlug string, queueSize int) *transport {
	t := &transport{
		apiKey:      apiKey,
		endpoint:    endpoint,
		projectSlug: projectSlug,
		queue:       make(chan envelope, queueSize),
		done:        make(chan struct{}),
		client:      &http.Client{Timeout: 5 * time.Second},
	}
	go t.run()
	return t
}

func (t *transport) enqueue(env envelope) bool {
	select {
	case t.queue <- env:
		return true
	default:
		return false
	}
}

func (t *transport) run() {
	defer close(t.done)
	for env := range t.queue {
		_ = t.send(env) // log failure silently
	}
}

func (t *transport) flush(timeout time.Duration) bool {
	// Drain by sending a sentinel that closes after queue is empty.
	// Simple approach: close the queue and wait for done with timeout.
	// We can't close here (flush may be called multiple times).
	// Instead: create a ticker-based drain check.
	deadline := time.Now().Add(timeout)
	for {
		if len(t.queue) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (t *transport) shutdown(timeout time.Duration) bool {
	close(t.queue)
	select {
	case <-t.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (t *transport) send(env envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BugBarn-Api-Key", t.apiKey)
	if t.projectSlug != "" {
		req.Header.Set("X-BugBarn-Project", t.projectSlug)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
