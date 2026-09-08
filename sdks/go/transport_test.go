package bugbarn

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransportQueueFull(t *testing.T) {
	// Built without newTransport, and therefore without its background
	// goroutine. That goroutine drains the queue as fast as items arrive, so
	// the previous version of this test — fill the channel, then assert the
	// next enqueue fails — was a race it usually won on an idle machine and
	// lost on a loaded one: run() had already taken an item off the channel
	// and freed a slot, and the test failed with no bug present. The
	// assertion is purely about enqueue's non-blocking send, which needs no
	// goroutine and no network at all.
	const capacity = 2
	tr := &transport{queue: make(chan envelope, capacity), done: make(chan struct{})}

	env := envelope{Timestamp: "now", SeverityText: "ERROR"}
	for i := 0; i < capacity; i++ {
		if !tr.enqueue(env) {
			t.Fatalf("enqueue %d returned false while the queue still had room", i)
		}
	}

	// Queue is now at capacity; next enqueue must return false.
	if tr.enqueue(env) {
		t.Fatal("expected enqueue to return false when queue is full")
	}
}

func TestTransportSend(t *testing.T) {
	received := make(chan *http.Request, 1)
	var body []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		received <- r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newTransport("my-api-key", srv.URL, "my-project", 8)
	defer tr.shutdown(2 * time.Second)

	env := envelope{
		Timestamp:    "2024-01-01T00:00:00Z",
		SeverityText: "ERROR",
		Body:         "test error",
		Exception:    exceptionBlock{Type: "Error", Message: "test error"},
		Sender:       senderBlock{SDK: sdkBlock{Name: sdkName, Version: sdkVersion}},
	}
	tr.enqueue(env)

	select {
	case req := <-received:
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected Content-Type: %s", ct)
		}
		if key := req.Header.Get("X-BugBarn-Api-Key"); key != "my-api-key" {
			t.Fatalf("unexpected api key: %s", key)
		}
		if proj := req.Header.Get("X-BugBarn-Project"); proj != "my-project" {
			t.Fatalf("unexpected project: %s", proj)
		}
		var parsed envelope
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if parsed.Body != "test error" {
			t.Fatalf("unexpected body: %s", parsed.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for request")
	}
}

func TestTransportShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newTransport("key", srv.URL, "", 16)
	env := envelope{Timestamp: "now", SeverityText: "ERROR"}
	for i := 0; i < 5; i++ {
		tr.enqueue(env)
	}

	drained := tr.shutdown(2 * time.Second)
	if !drained {
		t.Fatal("expected shutdown to complete within timeout")
	}

	// done channel must be closed after shutdown.
	select {
	case <-tr.done:
		// ok
	default:
		t.Fatal("done channel not closed after shutdown")
	}
}
