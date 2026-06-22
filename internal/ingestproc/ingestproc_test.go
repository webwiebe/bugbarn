package ingestproc

import (
	"context"
	"encoding/base64"
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
