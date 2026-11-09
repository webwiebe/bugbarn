package queue

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A failed LPUSH loses nothing: the spool keeps the record and the forwarder
// publishes it again. Publish returns the error and the caller logs it once
// (the forwarder at WARN), so Publish itself must not log at ERROR, which would
// file an issue through selflog for a retried publish (bugbarn#269).
func TestPublishFailureReturnsErrorWithoutErrorLog(t *testing.T) {
	q, mr := newTestQueue(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	mr.Close()
	err := q.Publish(context.Background(), []Item{{Kind: KindEvent}})
	if err == nil {
		t.Fatal("Publish with Redis down returned nil")
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Fatalf("Publish logged at ERROR for a retried publish:\n%s", buf.String())
	}
}
