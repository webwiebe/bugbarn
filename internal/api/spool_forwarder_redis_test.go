package api

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// TestRedisSpoolForwarderPublishes verifies the spec 007 producer: the reader's
// ingest spool, when given a Redis target, publishes spooled requests to the
// write queue as queue.Items (Kind by path) instead of forwarding over HTTP.
func TestRedisSpoolForwarderPublishes(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	defer q.Close()

	sf, err := NewRedisSpoolForwarder(t.TempDir(), q, 1<<20, slog.Default())
	if err != nil {
		t.Fatalf("NewRedisSpoolForwarder: %v", err)
	}
	defer sf.Close()

	cases := []struct {
		path string
		want string
		body string
	}{
		// Events are validated synchronously, so the body must be a valid event
		// payload (a JSON object). Logs are forwarded as-is.
		{"/api/v1/events", queue.KindEvent, `{"message":"PAYLOAD-event"}`},
		{"/api/v1/logs", queue.KindLog, "PAYLOAD-log"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bugbarn-Project", "svc")
		rec := httptest.NewRecorder()
		sf.Forward(rec, req)
		if rec.Code != 202 {
			t.Fatalf("%s: Forward status = %d, want 202", c.path, rec.Code)
		}
	}

	// Drain the spool — with a Redis target this publishes instead of HTTP.
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}

	for _, c := range cases {
		items, err := q.Consume(context.Background())
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("%s: got %d items, want 1", c.path, len(items))
		}
		got := items[0]
		if got.Kind != c.want {
			t.Errorf("%s: kind = %q, want %q", c.path, got.Kind, c.want)
		}
		if got.ProjectSlug != "svc" {
			t.Errorf("%s: project = %q, want svc", c.path, got.ProjectSlug)
		}
		body, _ := base64.StdEncoding.DecodeString(got.BodyBase64)
		if string(body) != c.body {
			t.Errorf("%s: body = %q", c.path, string(body))
		}
	}
}
