package selflog

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// report is one capture the handler would have sent to BugBarn.
type report struct {
	msg   string
	attrs map[string]any
}

type recorder struct {
	mu      sync.Mutex
	reports []report
}

func (r *recorder) capture(msg string, attrs map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, report{msg: msg, attrs: attrs})
}

func (r *recorder) all() []report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]report(nil), r.reports...)
}

func newTestHandler(buf *bytes.Buffer) (*Handler, *recorder) {
	rec := &recorder{}
	h := NewHandler(slog.NewJSONHandler(buf, nil))
	h.capture = rec.capture
	return h, rec
}

// TestErrorReportCarriesAttributes is the diagnosability half of BS2-107: the
// self-reported "ingest pipeline unhealthy" events carried only the message,
// so nobody could tell which environment raised them or why.
func TestErrorReportCarriesAttributes(t *testing.T) {
	var buf bytes.Buffer
	h, rec := newTestHandler(&buf)

	logger := slog.New(h).With("component", "ingest-health")
	logger.Error("ingest pipeline unhealthy",
		"environment", "production",
		"reasons", []string{"events queued but not persisted for 45m0s (queue depth 4, threshold 30m0s)"},
		"queue_depth", int64(4),
		"last_event_at", time.Date(2026, 10, 6, 17, 15, 0, 0, time.UTC),
		slog.Group("queue", "known", true),
	)

	got := rec.all()
	if len(got) != 1 || got[0].msg != "ingest pipeline unhealthy" {
		t.Fatalf("the message must stay unchanged so the issue keeps grouping, got %+v", got)
	}
	attrs := got[0].attrs
	want := map[string]any{
		"component":     "ingest-health",
		"environment":   "production",
		"queue_depth":   int64(4),
		"last_event_at": "2026-10-06T17:15:00Z",
		"queue.known":   true,
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Fatalf("attr %s: got %#v, want %#v (all: %v)", k, attrs[k], v, attrs)
		}
	}
	if reasons, ok := attrs["reasons"].([]string); !ok || len(reasons) != 1 {
		t.Fatalf("expected the reasons list, got %#v", attrs["reasons"])
	}
	if _, err := json.Marshal(attrs); err != nil {
		t.Fatalf("attributes must encode as JSON: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"msg":"ingest pipeline unhealthy"`)) {
		t.Fatal("the record must still reach the wrapped handler")
	}
}

func TestGroupedLoggerPrefixesKeys(t *testing.T) {
	var buf bytes.Buffer
	h, rec := newTestHandler(&buf)

	slog.New(h).WithGroup("consumer").With("batch", 3).Error("write failed", "kind", "security")

	attrs := rec.all()[0].attrs
	if attrs["consumer.batch"] != int64(3) || attrs["consumer.kind"] != "security" {
		t.Fatalf("expected group-prefixed keys, got %v", attrs)
	}
}

func TestErrorAttrKeepsMessageShape(t *testing.T) {
	var buf bytes.Buffer
	h, rec := newTestHandler(&buf)

	slog.New(h).Error("query last event time", "error", errors.New("disk I/O error"))

	got := rec.all()[0]
	if got.msg != "query last event time: disk I/O error" {
		t.Fatalf("unexpected message %q", got.msg)
	}
	if got.attrs["error"] != "disk I/O error" {
		t.Fatalf("expected the error attr as a string, got %#v", got.attrs["error"])
	}
}

func TestUnencodableValueIsStringified(t *testing.T) {
	var buf bytes.Buffer
	h, rec := newTestHandler(&buf)

	slog.New(h).Error("boom", "ch", make(chan int))

	attrs := rec.all()[0].attrs
	if _, ok := attrs["ch"].(string); !ok {
		t.Fatalf("expected a channel to be sent as a string, got %#v", attrs["ch"])
	}
	if _, err := json.Marshal(attrs); err != nil {
		t.Fatalf("attributes must encode as JSON: %v", err)
	}
}

func TestBelowErrorIsNotReported(t *testing.T) {
	var buf bytes.Buffer
	h, rec := newTestHandler(&buf)

	slog.New(h).Warn("ingest-health: unhealthy sample, waiting for confirmation", "queue_depth", 4)

	if n := len(rec.all()); n != 0 {
		t.Fatalf("WARN must not be reported, got %d reports", n)
	}
}
