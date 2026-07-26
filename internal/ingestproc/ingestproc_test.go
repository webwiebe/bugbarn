package ingestproc

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const fixturePath = "../../specs/001-personal-error-tracker/fixtures/example-event.json"

func newProcessor(t *testing.T) (*Processor, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	pub := service.NewEventPublisher(&domainevents.Bus{})
	// autoApprove=true: brand-new projects are created active so these tests
	// exercise the persist path. The pending/hold path is covered in held_test.go.
	return NewProcessor(store, pub, nil, true), store
}

func eventBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestPersistRecordSuccess(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	ctx := context.Background()

	res := proc.PersistRecord(ctx, spool.Record{
		IngestID:    "ing-1",
		ReceivedAt:  time.Now().UTC(),
		BodyBase64:  eventBody(t),
		ProjectSlug: "test-svc",
	})
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("outcome = %v, err = %v", res.Outcome, res.Err)
	}
	if res.Issue.ID == "" {
		t.Error("expected an issue to be created")
	}

	issues, err := store.ListIssues(projectCtx(t, ctx, store, "test-svc"))
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 1 {
		t.Errorf("got %d issues, want 1", len(issues))
	}
}

// projectCtx returns a context scoped to the named project so ListIssues does
// not fall back to the default project.
func projectCtx(t *testing.T, ctx context.Context, store *storage.Store, slug string) context.Context {
	t.Helper()
	proj, err := store.ProjectBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("project by slug %q: %v", slug, err)
	}
	return storage.WithProjectID(ctx, proj.ID)
}

func TestPersistRecordParseError(t *testing.T) {
	t.Parallel()
	proc, _ := newProcessor(t)
	res := proc.PersistRecord(context.Background(), spool.Record{
		IngestID:   "bad",
		BodyBase64: "not-valid-base64-$$$",
	})
	if res.Outcome != OutcomeParseError {
		t.Fatalf("outcome = %v, want OutcomeParseError", res.Outcome)
	}
}

func TestConsumerDrainsEventQueue(t *testing.T) {
	t.Parallel()
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

	proc, store := newProcessor(t)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)

	ctx := context.Background()
	if err := q.Publish(ctx, []queue.Item{{
		Kind:        queue.KindEvent,
		IngestID:    "ing-1",
		ReceivedAt:  time.Now().UTC(),
		ProjectSlug: "test-svc",
		BodyBase64:  eventBody(t),
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.Run(runCtx)

	// Wait until the queue drains and the event is persisted.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := q.Len(ctx)
		var issueCount int
		if proj, err := store.ProjectBySlug(ctx, "test-svc"); err == nil {
			issues, _ := store.ListIssues(storage.WithProjectID(ctx, proj.ID))
			issueCount = len(issues)
		}
		if n == 0 && issueCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("consumer did not drain: queue_len=%d issues=%d", n, issueCount)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConsumerDrainsLogQueue(t *testing.T) {
	t.Parallel()
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

	proc, store := newProcessor(t)
	logs := logsvc.New(store, nil)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logs, &mu, nil)

	ctx := context.Background()
	logBody := []byte(`{"logs":[{"level":"error","msg":"boom","reqId":"abc"}]}`)
	if err := q.Publish(ctx, []queue.Item{{
		Kind:        queue.KindLog,
		ReceivedAt:  time.Now().UTC(),
		ContentType: "application/json",
		ProjectSlug: "test-svc",
		BodyBase64:  base64.StdEncoding.EncodeToString(logBody),
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.Run(runCtx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := q.Len(ctx)
		var logCount int
		if proj, err := store.ProjectBySlug(ctx, "test-svc"); err == nil {
			entries, _ := store.ListLogEntries(ctx, proj.ID, 0, "", 50, 0)
			logCount = len(entries)
		}
		if n == 0 && logCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log consumer did not drain: queue_len=%d logs=%d", n, logCount)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Items are BRPOPped off the Redis list before they are persisted, so a
// shutdown that lands mid-batch used to lose them outright — noisily for the
// item being persisted ("drop event after persist error: context canceled",
// BS2-103) and silently for every item behind it. They must go back on the
// queue instead.
func TestConsumerRequeuesOnShutdown(t *testing.T) {
	t.Parallel()
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

	proc, store := newProcessor(t)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)

	ctx := context.Background()
	items := make([]queue.Item, 0, 4)
	for i := range 4 {
		items = append(items, queue.Item{
			Kind:        queue.KindEvent,
			IngestID:    fmt.Sprintf("ing-shutdown-%d", i),
			ReceivedAt:  time.Now().UTC(),
			ProjectSlug: "test-svc",
			BodyBase64:  eventBody(t),
		})
	}

	// Hand the whole batch to processBatch with an already-canceled context:
	// nothing can be persisted, so the entire batch must come back.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	c.processBatch(canceled, items)

	n, err := q.Len(ctx)
	if err != nil {
		t.Fatalf("len: %v", err)
	}
	if n == 0 {
		t.Fatal("shutdown lost the whole batch — nothing was requeued")
	}

	// And the items must come back intact, not as some placeholder.
	got, err := q.Consume(ctx)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(got) != len(items) {
		t.Fatalf("requeued %d items, want %d", len(got), len(items))
	}
	for i, it := range got {
		if it.IngestID != items[i].IngestID {
			t.Errorf("item %d: ingest_id %q, want %q", i, it.IngestID, items[i].IngestID)
		}
		if it.BodyBase64 != items[i].BodyBase64 {
			t.Errorf("item %d: body not preserved", i)
		}
	}
}

// A final disposition must never be requeued, or an unparseable item would
// cycle through the queue forever.
func TestHandledOutcomes(t *testing.T) {
	t.Parallel()
	for _, o := range []string{"success", "held", "parse_error", "decode_error", "dropped", "empty", "unknown_kind"} {
		if !handled(o) {
			t.Errorf("%q should be final", o)
		}
	}
	for _, o := range []string{"persist_error", "transient_drop", "retry_exhausted", "insert_error"} {
		if handled(o) {
			t.Errorf("%q is unfinished and must be requeued", o)
		}
	}
}

// An empty slug is unroutable: there is no project to attach the logs to, so
// retrying cannot help and the item is a permanent drop. A slug that is present
// but momentarily unresolvable is a different animal — that is a database blip,
// the same class the log insert already retries, and discarding it threw away
// data we had accepted (BS2-120).
func TestResolveProjectSeparatesUnroutableFromTransient(t *testing.T) {
	t.Parallel()
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

	proc, store := newProcessor(t)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)

	t.Run("no slug is a permanent drop", func(t *testing.T) {
		_, outcome, ok := c.resolveProject(context.Background(), queue.Item{IngestID: "ing-noslug"})
		if ok {
			t.Fatal("an empty slug must not resolve")
		}
		if outcome != "dropped" {
			t.Errorf("outcome = %q, want %q", outcome, "dropped")
		}
		if !handled(outcome) {
			t.Error("an unroutable item is final and must not be requeued forever")
		}
	})

	t.Run("a real slug resolves", func(t *testing.T) {
		proj, outcome, ok := c.resolveProject(context.Background(), queue.Item{
			IngestID: "ing-ok", ProjectSlug: "test-svc",
		})
		if !ok {
			t.Fatalf("expected resolution, got outcome %q", outcome)
		}
		if proj.ID == 0 {
			t.Error("resolved project has no id")
		}
	})

	t.Run("shutdown mid-resolution is not a permanent drop", func(t *testing.T) {
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_, outcome, ok := c.resolveProject(canceled, queue.Item{
			IngestID: "ing-cancel", ProjectSlug: "some-unresolvable-slug",
		})
		if ok {
			return // resolution won the race; nothing to assert
		}
		if handled(outcome) {
			t.Errorf("outcome %q would discard the item on shutdown; it must be requeued", outcome)
		}
	})
}
