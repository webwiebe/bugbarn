package ingestproc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func newReplayQueue(t *testing.T) *queue.RedisQueue {
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
	return q
}

func publishOneEvent(t *testing.T, q *queue.RedisQueue) {
	t.Helper()
	if err := q.Publish(context.Background(), []queue.Item{{
		Kind:        queue.KindEvent,
		ReceivedAt:  time.Now().UTC(),
		ProjectSlug: "test-svc",
		BodyBase64:  eventBody(t),
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// storedEvents returns the issue's lifetime event_count and its stored event
// rows, or -1, -1 while no issue exists yet.
func storedEvents(t *testing.T, store *storage.Store) (eventCount, rows int) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.ProjectBySlug(ctx, "test-svc"); err != nil {
		return -1, -1
	}
	pctx := projectCtx(t, ctx, store, "test-svc")
	issues, err := store.ListIssues(pctx)
	if err != nil || len(issues) == 0 {
		return -1, -1
	}
	events, _, err := store.ListIssueEvents(pctx, issues[0].ID, 100, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return issues[0].EventCount, len(events)
}

// waitDrained waits until the write queue and the processing list are empty
// and the issue holds want events.
func waitDrained(t *testing.T, q *queue.RedisQueue, store *storage.Store, want int) {
	t.Helper()
	ctx := context.Background()
	key := queue.ProcessingKey(queue.DefaultWriter)
	deadline := time.Now().Add(10 * time.Second)
	for {
		queued, _ := q.Len(ctx)
		held, _ := q.ProcessingLen(ctx, key)
		count, rows := storedEvents(t, store)
		if queued == 0 && held == 0 && count == want && rows == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after restart: queued=%d processing=%d event_count=%d rows=%d, want 0/0/%d/%d",
				queued, held, count, rows, want, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A writer that took an entry off the write queue and was killed before it
// stored anything loses nothing: the entry waits in its processing list and
// the next start stores it (bugbarn#257). With BRPOP the entry was gone.
func TestWriterKilledBetweenPopAndCommitLosesNothing(t *testing.T) {
	t.Parallel()
	q := newReplayQueue(t)
	proc, store := newProcessor(t)
	publishOneEvent(t, q)

	// The first writer takes the entry and dies before it reaches the store.
	d, err := q.Reserve(context.Background(), queue.ProcessingKey(queue.DefaultWriter))
	if err != nil || d == nil {
		t.Fatalf("reserve: %v, %v", d, err)
	}
	if count, _ := storedEvents(t, store); count != -1 {
		t.Fatalf("nothing should be stored before the restart, event_count=%d", count)
	}

	// The next writer starts against the same Redis and store.
	var mu sync.Mutex
	c := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)
	t.Cleanup(c.Close)
	runConsumer(t, c)

	waitDrained(t, q, store, 1)
}

// A writer killed after its commit and before the ack stores the entry again
// on the next start. Queue items carry no stable event id on the production
// path, so the replay cannot skip it: the event gets a second row and the
// issue's event_count over-counts by one per event in that entry. This is the
// at-least-once cost bugbarn#257 accepts.
func TestWriterKilledBeforeAckStoresTheEntryTwice(t *testing.T) {
	t.Parallel()
	q := newReplayQueue(t)
	proc, store := newProcessor(t)
	publishOneEvent(t, q)

	var mu sync.Mutex
	first := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)
	t.Cleanup(first.Close)
	d, err := q.Reserve(context.Background(), queue.ProcessingKey(queue.DefaultWriter))
	if err != nil || d == nil {
		t.Fatalf("reserve: %v, %v", d, err)
	}
	if !first.processBatch(context.Background(), d.Items) {
		t.Fatal("processBatch reported the entry unfinished")
	}
	if count, rows := storedEvents(t, store); count != 1 || rows != 1 {
		t.Fatalf("after the commit: event_count=%d rows=%d, want 1/1", count, rows)
	}
	// Killed here: no ack.

	second := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)
	t.Cleanup(second.Close)
	runConsumer(t, second)

	waitDrained(t, q, store, 2)
}

// The normal path acks: once the batch is stored the processing list is
// empty (waitDrained checks it), so a restart has nothing to replay.
func TestConsumerAcksAStoredEntry(t *testing.T) {
	t.Parallel()
	q := newReplayQueue(t)
	proc, store := newProcessor(t)
	var mu sync.Mutex
	c := NewConsumer(q, proc, logsvc.New(store, nil), &mu, nil)
	t.Cleanup(c.Close)
	runConsumer(t, c)

	publishOneEvent(t, q)
	waitDrained(t, q, store, 1)
}
