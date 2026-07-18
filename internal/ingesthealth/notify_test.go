package ingesthealth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
)

// fakeNotifier records deliveries; err (when set) is returned to the monitor.
type fakeNotifier struct {
	mu    sync.Mutex
	name  string
	err   error
	calls []Snapshot
	envs  []string
}

func (f *fakeNotifier) Name() string { return f.name }

func (f *fakeNotifier) Notify(_ context.Context, env string, snap Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, snap)
	f.envs = append(f.envs, env)
	return f.err
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestNotifiesOutOfBandOnStall is the regression for issue #165: the alert that
// says ingest is broken must leave over a channel that is not ingest. Before
// this, the only report was an ERROR log that selflog queued behind the very
// backlog it described, so a 13h stall looked like a quiet dashboard.
func TestNotifiesOutOfBandOnStall(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	f := &fakeNotifier{name: "fake"}
	m := New(Config{StaleAfter: 30 * time.Minute, Environment: "production"}, baseDeps(now.Add(-13*time.Hour), 103_062), nil)
	m.now = func() time.Time { return now }
	m.AddNotifier(f)

	m.sample(context.Background())

	if f.count() != 1 {
		t.Fatalf("expected exactly one out-of-band delivery, got %d", f.count())
	}
	got := f.calls[0]
	if got.Healthy {
		t.Fatal("expected the delivered snapshot to be the unhealthy one")
	}
	if got.QueueDepth != 103_062 {
		t.Fatalf("expected the backlog depth in the alert, got %d", got.QueueDepth)
	}
	if f.envs[0] != "production" {
		t.Fatalf("expected the environment to label the alert, got %q", f.envs[0])
	}
}

func TestHealthySampleDoesNotNotify(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	f := &fakeNotifier{name: "fake"}
	m := New(Config{StaleAfter: 30 * time.Minute, MaxQueueDepth: 1000}, baseDeps(now.Add(-time.Minute), 5), nil)
	m.now = func() time.Time { return now }
	m.AddNotifier(f)

	m.sample(context.Background())

	if f.count() != 0 {
		t.Fatalf("a healthy pipeline must not page anyone, got %d deliveries", f.count())
	}
}

// TestNotifyShareTheAlertThrottle keeps a sustained outage from mailing every
// interval: out-of-band delivery rides the same AlertEvery window as the log.
func TestNotifyShareTheAlertThrottle(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	f := &fakeNotifier{name: "fake"}
	m := New(Config{StaleAfter: time.Minute, AlertEvery: 15 * time.Minute}, baseDeps(now.Add(-time.Hour), 2), nil)
	cur := now
	m.now = func() time.Time { return cur }
	m.AddNotifier(f)

	m.sample(context.Background())
	cur = now.Add(5 * time.Minute)
	m.sample(context.Background())
	if f.count() != 1 {
		t.Fatalf("expected the throttle to suppress the second delivery, got %d", f.count())
	}

	cur = now.Add(20 * time.Minute)
	m.sample(context.Background())
	if f.count() != 2 {
		t.Fatalf("expected delivery to resume after the throttle window, got %d", f.count())
	}
}

// TestOneFailingChannelDoesNotBlockAnother: a dead SMTP server must not swallow
// the webhook — the point of having two channels is that either one suffices.
func TestOneFailingChannelDoesNotBlockAnother(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	bad := &fakeNotifier{name: "bad", err: errors.New("smtp unreachable")}
	good := &fakeNotifier{name: "good"}
	m := New(Config{StaleAfter: time.Minute}, baseDeps(now.Add(-time.Hour), 2), nil)
	m.now = func() time.Time { return now }
	m.AddNotifier(bad, good)

	m.sample(context.Background())

	if good.count() != 1 {
		t.Fatalf("a failing channel must not stop a working one, got %d", good.count())
	}
}

// TestAddNotifierSkipsUnconfigured: the constructors return a typed nil when a
// channel is not configured, which is not == nil once boxed in an interface.
// Wiring one unconfigured must not panic on the next stall.
func TestAddNotifierSkipsUnconfigured(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m := New(Config{StaleAfter: time.Minute}, baseDeps(now.Add(-time.Hour), 2), nil)
	m.now = func() time.Time { return now }
	m.AddNotifier(NewWebhookNotifier(""), NewEmailNotifier(digest.MailConfig{}, ""))

	if len(m.notifiers) != 0 {
		t.Fatalf("expected unconfigured channels to be dropped, got %d", len(m.notifiers))
	}
	m.sample(context.Background()) // must not panic
}

func TestWebhookNotifierPostsSnapshot(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
		ct   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ = io.ReadAll(r.Body)
		ct = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL)
	snap := Snapshot{
		Sampled:             true,
		SampledAt:           time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Reasons:             []string{"write-queue backlog 103062 over threshold 50000"},
		LastEventAgeSeconds: 45989,
		QueueDepth:          103_062,
	}
	if err := n.Notify(context.Background(), "production", snap); err != nil {
		t.Fatalf("notify: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if ct != "application/json" {
		t.Fatalf("expected a JSON content type, got %q", ct)
	}
	var got alertPayload
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.Environment != "production" || got.QueueDepth != 103_062 || got.LastEventAgeSeconds != 45989 {
		t.Fatalf("payload lost the diagnostic facts: %+v", got)
	}
	if len(got.Reasons) != 1 {
		t.Fatalf("expected the reason to survive, got %v", got.Reasons)
	}
}

// TestSimulatedStallDeliversOverTheWire runs the whole path — monitor loop,
// real notifier, real HTTP — against a simulated stall, which is the acceptance
// check for issue #165: pause ingest, confirm an alert lands somewhere that is
// not the write queue.
func TestSimulatedStallDeliversOverTheWire(t *testing.T) {
	got := make(chan alertPayload, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p alertPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Ingest "stalled" 13h ago and the queue has backed up, exactly as in the
	// 2026-07-16 incident.
	now := time.Date(2026, 7, 16, 11, 20, 0, 0, time.UTC)
	m := New(Config{
		Interval:    time.Millisecond,
		StaleAfter:  30 * time.Minute,
		Environment: "production",
	}, baseDeps(now.Add(-13*time.Hour), 103_062), nil)
	m.now = func() time.Time { return now }
	m.AddNotifier(NewWebhookNotifier(srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Start(ctx)

	select {
	case p := <-got:
		if p.Environment != "production" || p.QueueDepth != 103_062 {
			t.Fatalf("alert reached the wire but lost its facts: %+v", p)
		}
		if p.LastEventAgeSeconds < 46_000 {
			t.Fatalf("expected the ~13h stall age, got %v", p.LastEventAgeSeconds)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no out-of-band alert arrived for a stalled pipeline")
	}
}

func TestWebhookNotifierErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := NewWebhookNotifier(srv.URL).Notify(context.Background(), "", Snapshot{Sampled: true})
	if err == nil {
		t.Fatal("expected a non-2xx webhook response to error, not fail silently")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected the status in the error, got %v", err)
	}
}

func TestNewWebhookNotifierBlankURL(t *testing.T) {
	if n := NewWebhookNotifier("   "); n != nil {
		t.Fatal("expected a blank URL to yield no notifier")
	}
}

func TestNewEmailNotifierRequiresSMTPAndRecipient(t *testing.T) {
	full := digest.MailConfig{Enabled: true, Host: "smtp.example.com", Port: 587, From: "bb@example.com"}
	cases := []struct {
		name string
		mail digest.MailConfig
		to   string
		want bool
	}{
		{"configured", full, "ops@example.com", true},
		{"no recipient", full, "  ", false},
		{"smtp disabled", digest.MailConfig{Host: "smtp.example.com"}, "ops@example.com", false},
		{"no host", digest.MailConfig{Enabled: true}, "ops@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewEmailNotifier(tc.mail, tc.to) != nil
			if got != tc.want {
				t.Fatalf("NewEmailNotifier configured=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestEmailBodyCarriesDiagnostics: the mail must stand on its own, since the
// dashboard it would otherwise link to is exactly what is not updating.
func TestEmailBodyCarriesDiagnostics(t *testing.T) {
	snap := Snapshot{
		Sampled:             true,
		SampledAt:           time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		LastEventAt:         time.Date(2026, 7, 15, 22, 30, 10, 0, time.UTC),
		LastEventAgeSeconds: 45989,
		QueueDepth:          103_062,
		QueueDepthKnown:     true,
		Reasons:             []string{"write-queue backlog 103062 over threshold 50000"},
	}
	for _, body := range []string{plainBody("production", snap), htmlBody("production", snap)} {
		for _, want := range []string{"production", "103062", "12h46m29s", "2026-07-15T22:30:10Z"} {
			if !strings.Contains(body, want) {
				t.Fatalf("body missing %q:\n%s", want, body)
			}
		}
	}
}

// TestHTMLBodyEscapes: reasons are built from queue/DB values, so the HTML part
// must escape rather than interpolate them raw.
func TestHTMLBodyEscapes(t *testing.T) {
	snap := Snapshot{Sampled: true, Reasons: []string{`<script>alert("x")</script>`}}
	if got := htmlBody("", snap); strings.Contains(got, "<script>") {
		t.Fatalf("expected the reason to be escaped, got %s", got)
	}
}
