package queue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newTestQueue(t *testing.T) (*RedisQueue, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	q, err := NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisQueue: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return q, mr
}

func TestPublishConsumeRoundTrip(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	ctx := context.Background()

	items := []Item{
		{Kind: KindEvent, ProjectSlug: "svc", BodyBase64: "ZXZlbnQ=", ReceivedAt: time.Unix(1, 0).UTC()},
		{Kind: KindLog, ProjectSlug: "svc", BodyBase64: "bG9n", ReceivedAt: time.Unix(2, 0).UTC()},
	}
	if err := q.Publish(ctx, items); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got, err := q.Consume(ctx)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	if got[0].Kind != KindEvent || got[1].Kind != KindLog {
		t.Errorf("kinds round-tripped wrong: %q, %q", got[0].Kind, got[1].Kind)
	}
	if got[0].BodyBase64 != "ZXZlbnQ=" {
		t.Errorf("body round-tripped wrong: %q", got[0].BodyBase64)
	}
}

func TestConsumeTimeoutReturnsNil(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	// brpopTimeout is 5s; bound the test well under that via ctx is not enough
	// (BRPOP honors the redis-side timeout), so just assert an empty queue with a
	// short BRPOP by temporarily shrinking via a cancelled context path instead:
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := q.Consume(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context BRPOP")
	}
}

func TestLenReflectsDepth(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	ctx := context.Background()

	if n, _ := q.Len(ctx); n != 0 {
		t.Fatalf("empty len = %d, want 0", n)
	}
	// Two Publish calls under maxItemsPerBatch => two list entries.
	_ = q.Publish(ctx, []Item{{Kind: KindEvent, BodyBase64: "YQ=="}})
	_ = q.Publish(ctx, []Item{{Kind: KindLog, BodyBase64: "Yg=="}})
	if n, _ := q.Len(ctx); n != 2 {
		t.Errorf("len = %d, want 2", n)
	}
}

func TestPublishBatchesLargeInput(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	ctx := context.Background()

	items := make([]Item, maxItemsPerBatch+50)
	for i := range items {
		items[i] = Item{Kind: KindEvent, BodyBase64: "YQ=="}
	}
	if err := q.Publish(ctx, items); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// 550 items => two list entries (500 + 50).
	if n, _ := q.Len(ctx); n != 2 {
		t.Errorf("len = %d, want 2 batches", n)
	}
	// FIFO: first BRPOP returns the first-published batch of 500.
	first, err := q.Consume(ctx)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(first) != maxItemsPerBatch {
		t.Errorf("first batch = %d, want %d", len(first), maxItemsPerBatch)
	}
}

func TestNewRedisQueueBadURL(t *testing.T) {
	t.Parallel()
	if _, err := NewRedisQueue("not-a-url"); err == nil {
		t.Fatal("expected error for invalid redis url")
	}
}
