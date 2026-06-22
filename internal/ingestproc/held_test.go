package ingestproc

import (
	"context"
	"encoding/base64"
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

// pendingProcessor builds a processor with autoApprove=false so brand-new
// projects are created pending and their ingest is held.
func pendingProcessor(t *testing.T) (*Processor, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	pub := service.NewEventPublisher(&domainevents.Bus{})
	return NewProcessor(store, pub, nil, false), store
}

func TestPersistRecordHeldForPendingProject(t *testing.T) {
	t.Parallel()
	proc, store := pendingProcessor(t)
	ctx := context.Background()

	res := proc.PersistRecord(ctx, spool.Record{
		IngestID:    "ing-1",
		ReceivedAt:  time.Now().UTC(),
		BodyBase64:  eventBody(t),
		ProjectSlug: "pending-svc",
	})
	if res.Outcome != OutcomeHeld {
		t.Fatalf("outcome = %v, want OutcomeHeld (err=%v)", res.Outcome, res.Err)
	}

	proj, err := store.ProjectBySlug(ctx, "pending-svc")
	if err != nil {
		t.Fatalf("project by slug: %v", err)
	}
	if proj.Status != "pending" {
		t.Errorf("project status = %q, want pending", proj.Status)
	}
	n, err := store.CountHeldByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("count held: %v", err)
	}
	if n != 1 {
		t.Errorf("held count = %d, want 1", n)
	}
	issues, err := store.ListIssues(storage.WithProjectID(ctx, proj.ID))
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues, want 0 (held, not persisted)", len(issues))
	}
}

func TestReplayHeldEventsOnApproval(t *testing.T) {
	t.Parallel()
	proc, store := pendingProcessor(t)
	ctx := context.Background()

	// Two events arrive while pending → both held.
	for i := 0; i < 2; i++ {
		res := proc.PersistRecord(ctx, spool.Record{
			IngestID:    "ing",
			ReceivedAt:  time.Now().UTC(),
			BodyBase64:  eventBody(t),
			ProjectSlug: "pending-svc",
		})
		if res.Outcome != OutcomeHeld {
			t.Fatalf("event %d outcome = %v, want OutcomeHeld", i, res.Outcome)
		}
	}
	proj, err := store.ProjectBySlug(ctx, "pending-svc")
	if err != nil {
		t.Fatalf("project by slug: %v", err)
	}

	// Approve and drain.
	if err := store.ApproveProject(ctx, "pending-svc"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	replayer := NewReplayer(store, proc, logsvc.New(store, nil), nil)
	got, err := replayer.ReplayHeld(ctx, proj.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got != 2 {
		t.Errorf("replayed = %d, want 2", got)
	}

	n, _ := store.CountHeldByProject(ctx, proj.ID)
	if n != 0 {
		t.Errorf("held count after replay = %d, want 0", n)
	}
	// Both events share a fingerprint → one issue, two events on it.
	issues, err := store.ListIssues(storage.WithProjectID(ctx, proj.ID))
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].EventCount != 2 {
		t.Errorf("issue event count = %d, want 2", issues[0].EventCount)
	}
}

func TestConsumerHoldsAndReplaysLog(t *testing.T) {
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

	proc, store := pendingProcessor(t)
	logs := logsvc.New(store, nil)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logs, &mu, nil)

	ctx := context.Background()
	logBody := []byte(`{"logs":[{"level":"error","msg":"boom","reqId":"abc"}]}`)
	if err := q.Publish(ctx, []queue.Item{{
		Kind:        queue.KindLog,
		ReceivedAt:  time.Now().UTC(),
		ContentType: "application/json",
		ProjectSlug: "pending-svc",
		BodyBase64:  base64.StdEncoding.EncodeToString(logBody),
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	go c.Run(runCtx)

	// Wait for the queue to drain into held_events (not log_entries).
	var proj storage.Project
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, _ := q.Len(ctx)
		if p, err := store.ProjectBySlug(ctx, "pending-svc"); err == nil {
			proj = p
			held, _ := store.CountHeldByProject(ctx, proj.ID)
			if n == 0 && held == 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("consumer did not hold log: queue_len=%d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	// No log entries persisted while pending.
	entries, _ := store.ListLogEntries(ctx, proj.ID, 0, "", 50, 0)
	if len(entries) != 0 {
		t.Errorf("got %d log entries while pending, want 0", len(entries))
	}

	// Approve → replay → log is ingested.
	if err := store.ApproveProject(ctx, "pending-svc"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	replayer := NewReplayer(store, proc, logs, nil)
	if _, err := replayer.ReplayHeld(ctx, proj.ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	entries, _ = store.ListLogEntries(ctx, proj.ID, 0, "", 50, 0)
	if len(entries) != 1 {
		t.Errorf("got %d log entries after approval, want 1", len(entries))
	}
}
