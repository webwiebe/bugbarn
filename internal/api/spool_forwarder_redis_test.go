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

// newTestRedisForwarder builds a Redis-backed forwarder over a throwaway
// miniredis, returning both so tests can assert on what was published.
func newTestRedisForwarder(t *testing.T) (*SpoolForwarder, *queue.RedisQueue) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	sf, err := NewRedisSpoolForwarder(t.TempDir(), q, 1<<20, slog.Default())
	if err != nil {
		t.Fatalf("NewRedisSpoolForwarder: %v", err)
	}
	t.Cleanup(func() { sf.Close() })
	return sf, q
}

// TestRedisSpoolForwarderResolvesProjectFromAPIKey is the regression test for
// issue #164: a log client authenticating with a project-scoped API key and no
// X-BugBarn-Project header published an item with an empty slug, which the
// consumer then dropped — ~91% of production log ingest, lost behind a 202.
func TestRedisSpoolForwarderResolvesProjectFromAPIKey(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	sf.SetProjectResolver(func(_ context.Context, apiKey string) string {
		if apiKey == "scoped-key" {
			return "svc"
		}
		return ""
	})

	req := httptest.NewRequest("POST", "/api/v1/logs", strings.NewReader("PAYLOAD-log"))
	req.Header.Set("X-Bugbarn-Api-Key", "scoped-key")
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != 202 {
		t.Fatalf("Forward status = %d, want 202", rec.Code)
	}
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}

	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].ProjectSlug != "svc" {
		t.Errorf("project = %q, want svc — key unresolved, consumer would drop this", items[0].ProjectSlug)
	}
}

// TestRedisSpoolForwarderHeaderBeatsAPIKey: an explicit header is the caller's
// override and wins over the project the key is scoped to.
func TestRedisSpoolForwarderHeaderBeatsAPIKey(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	sf.SetProjectResolver(func(context.Context, string) string { return "from-key" })

	req := httptest.NewRequest("POST", "/api/v1/logs", strings.NewReader("PAYLOAD-log"))
	req.Header.Set("X-Bugbarn-Api-Key", "scoped-key")
	req.Header.Set("X-Bugbarn-Project", "from-header")
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != 202 {
		t.Fatalf("Forward status = %d, want 202", rec.Code)
	}
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(items) != 1 || items[0].ProjectSlug != "from-header" {
		t.Fatalf("items = %+v, want one item for from-header", items)
	}
}

// TestRedisSpoolForwarderRejectsLogsWithoutProject: when neither the header nor
// the key names a project, reject at ingest. Accepting with a 202 and dropping
// in the consumer loses the batch without the client ever learning. Mirrors the
// direct path, which 400s on the same condition.
func TestRedisSpoolForwarderRejectsLogsWithoutProject(t *testing.T) {
	sf, _ := newTestRedisForwarder(t)
	sf.SetProjectResolver(func(context.Context, string) string { return "" })

	req := httptest.NewRequest("POST", "/api/v1/logs", strings.NewReader("PAYLOAD-log"))
	req.Header.Set("X-Bugbarn-Api-Key", "global-key")
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != 400 {
		t.Fatalf("Forward status = %d, want 400", rec.Code)
	}
	if pending := sf.Pending(); pending != 0 {
		t.Errorf("Pending = %d, want 0 — a rejected log must not be spooled", pending)
	}
}

// TestRedisSpoolForwarderEventsAllowEmptyProject: events, unlike logs, still
// pass with no project — the consumer falls them back to the Default Project
// rather than dropping them. That asymmetry is deliberate.
func TestRedisSpoolForwarderEventsAllowEmptyProject(t *testing.T) {
	sf, q := newTestRedisForwarder(t)

	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{"message":"boom"}`))
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != 202 {
		t.Fatalf("Forward status = %d, want 202", rec.Code)
	}
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(items) != 1 || items[0].Kind != queue.KindEvent {
		t.Fatalf("items = %+v, want one event item", items)
	}
}
