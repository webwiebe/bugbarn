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

// levelRecorder keeps the log records at WARN and above that carry a
// write-queue error (their text carries the "queue: " prefix), by level, and
// signals the first record of each level.
type levelRecorder struct {
	mu       sync.Mutex
	warns    []string
	errors   []string
	firstWrn chan struct{}
	firstErr chan struct{}
	onceWrn  sync.Once
	onceErr  sync.Once
}

func newLevelRecorder() *levelRecorder {
	return &levelRecorder{firstWrn: make(chan struct{}), firstErr: make(chan struct{})}
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *levelRecorder) WithGroup(string) slog.Handler            { return h }

func (h *levelRecorder) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelWarn {
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
	defer h.mu.Unlock()
	if r.Level >= slog.LevelError {
		h.errors = append(h.errors, text)
		h.onceErr.Do(func() { close(h.firstErr) })
		return nil
	}
	h.warns = append(h.warns, text)
	h.onceWrn.Do(func() { close(h.firstWrn) })
	return nil
}

// runAgainstDeadRedis runs a consumer whose Redis is gone, so every read fails,
// and returns it with a stop function that ends it and waits. The queue's own
// logs go to the default logger, so the test records both that and the
// consumer's logger. Not parallel: it replaces the default logger.
func runAgainstDeadRedis(t *testing.T, rec *levelRecorder) (stop func()) {
	t.Helper()
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
	mr.Close()

	c := NewConsumer(q, nil, nil, nil, slog.New(rec))
	t.Cleanup(c.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

// One failed write-queue read is one WARN record and no ERROR: the entry stays
// in the processing list and replays, so nothing is lost, and each ERROR files
// an issue through selflog (bugbarn#276: BS2-163 was one BLMOVE timeout). It is
// also one record, not two (bugbarn#255: BS2-159 and BS2-160 were duplicates).
func TestOneQueueReadFailureLogsOneWarn(t *testing.T) {
	rec := newLevelRecorder()
	stop := runAgainstDeadRedis(t, rec)

	select {
	case <-rec.firstWrn:
	case <-time.After(30 * time.Second):
		stop()
		t.Fatal("no warning was logged for the failed read")
	}
	// The consumer is now in its retry backoff; stopping ends it before a second
	// attempt.
	stop()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.warns) != 1 || len(rec.errors) != 0 {
		t.Fatalf("one failed read logged %d WARN and %d ERROR records, want 1 and 0:\nwarn: %s\nerror: %s",
			len(rec.warns), len(rec.errors), strings.Join(rec.warns, "\n"), strings.Join(rec.errors, "\n"))
	}
}

// An outage that outlasts consumerErrorAfter failures in a row logs one ERROR,
// and the failures before it log WARN. Later failures of the same outage add
// no second ERROR.
func TestQueueOutageLogsOneError(t *testing.T) {
	rec := newLevelRecorder()
	stop := runAgainstDeadRedis(t, rec)

	select {
	case <-rec.firstErr:
	case <-time.After(60 * time.Second):
		stop()
		t.Fatal("no error was logged for an outage")
	}
	stop()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.errors) != 1 {
		t.Fatalf("an outage logged %d ERROR records, want 1:\n%s", len(rec.errors), strings.Join(rec.errors, "\n"))
	}
	if len(rec.warns) != consumerErrorAfter-1 {
		t.Fatalf("an outage logged %d WARN records before the ERROR, want %d", len(rec.warns), consumerErrorAfter-1)
	}
}

// A good read ends the outage: the failure count restarts, so the next outage
// logs its own ERROR after consumerErrorAfter failures instead of right away.
func TestGoodReadResetsTheFailureCount(t *testing.T) {
	t.Parallel()

	q := newReplayQueue(t)
	c := NewConsumer(q, nil, nil, nil, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	t.Cleanup(c.Close)
	c.failures = consumerErrorAfter - 1

	// The write queue is empty, so Reserve returns after its idle timeout.
	if !c.consumeOne(context.Background()) {
		t.Fatal("consumeOne reported a failed read on a healthy queue")
	}
	if c.failures != 0 {
		t.Fatalf("failures = %d after a good read, want 0", c.failures)
	}
}
