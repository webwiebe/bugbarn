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
	// Use a non-listening address so sends fail fast without blocking.
	// The transport background goroutine will error on each send and loop,
	// keeping items in the queue long enough to test back-pressure.
	const cap = 2
	tr := newTransport("key", "http://127.0.0.1:1", "", cap)
	defer tr.shutdown(200 * time.Millisecond)

	env := envelope{Timestamp: "now", SeverityText: "ERROR"}

	// Fill the buffered channel directly (bypass the goroutine draining it).
	tr.queue <- env
	tr.queue <- env

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
