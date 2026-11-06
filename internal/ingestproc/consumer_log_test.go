package ingestproc

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// errorRecorder keeps the ERROR records about a write-queue error (their text
// carries the "queue: " prefix) and signals the first one.
type errorRecorder struct {
	mu      sync.Mutex
	records []string
	first   chan struct{}
	once    sync.Once
}

func (h *errorRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *errorRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *errorRecorder) WithGroup(string) slog.Handler            { return h }

func (h *errorRecorder) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelError {
		return nil
	}
	text := r.Message
	r.Attrs(func(a slog.Attr) bool {
		text += " " + a.Value.String()
		return true
	})
	if !strings.Contains(text, "queue: ") {
		return nil
	}
	h.mu.Lock()
	h.records = append(h.records, text)
	h.mu.Unlock()
	h.once.Do(func() { close(h.first) })
	return nil
}

// One failed write-queue read is one ERROR record. Each ERROR files an issue through
// selflog, so a second record for the same failure files a duplicate issue
// (bugbarn#255: BS2-159 and BS2-160). The queue's own logs go to the default
// logger, so the test records both that and the consumer's logger.
//
// Not parallel: it replaces the default logger.
func TestOneQueueReadFailureLogsOneError(t *testing.T) {
	rec := &errorRecorder{first: make(chan struct{})}
	prev := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(prev) })

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	q, err := queue.NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	// With Redis gone every read fails.
	mr.Close()

	c := NewConsumer(q, nil, nil, nil, slog.New(rec))
	t.Cleanup(c.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	select {
	case <-rec.first:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("no error was logged for the failed read")
	}
	// The consumer is now in its retry backoff; cancel ends it before a second
	// attempt.
	cancel()
	<-done

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.records) != 1 {
		t.Fatalf("one failed read logged %d ERROR records, want 1:\n%s",
			len(rec.records), strings.Join(rec.records, "\n"))
	}
}
