package queue

import (
	"context"
	"testing"
)

func TestReserveHoldsTheEntryUntilAck(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	ctx := context.Background()
	key := ProcessingKey(DefaultWriter)

	if err := q.Publish(ctx, []Item{{Kind: KindEvent, ProjectSlug: "svc", BodyBase64: "ZQ=="}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	d, err := q.Reserve(ctx, key)
	if err != nil || d == nil {
		t.Fatalf("Reserve: %v, %v", d, err)
	}
	if len(d.Items) != 1 || d.Items[0].BodyBase64 != "ZQ==" {
		t.Fatalf("Reserve items = %+v", d.Items)
	}
	if n, _ := q.Len(ctx); n != 0 {
		t.Fatalf("write queue len = %d, want 0", n)
	}
	if n, _ := q.ProcessingLen(ctx, key); n != 1 {
		t.Fatalf("processing len = %d before ack, want 1", n)
	}
	if err := q.Ack(ctx, d); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if n, _ := q.ProcessingLen(ctx, key); n != 0 {
		t.Fatalf("processing len = %d after ack, want 0", n)
	}
}

func TestReserveIdleReturnsNil(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	// Blocks for brpopTimeout on an empty queue.
	d, err := q.Reserve(context.Background(), ProcessingKey(DefaultWriter))
	if err != nil || d != nil {
		t.Fatalf("idle Reserve = %v, %v; want nil, nil", d, err)
	}
}

// Entries left in the processing list go back to the consuming end of the
// write queue, oldest first, ahead of entries that were never taken.
func TestRecoverProcessingReplaysOldestFirst(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	ctx := context.Background()
	key := ProcessingKey(DefaultWriter)

	for _, body := range []string{"a", "b", "c"} {
		if err := q.Publish(ctx, []Item{{Kind: KindEvent, BodyBase64: body}}); err != nil {
			t.Fatalf("Publish %s: %v", body, err)
		}
	}
	// A writer took a and b and acked neither; c was never taken.
	for range 2 {
		if d, err := q.Reserve(ctx, key); err != nil || d == nil {
			t.Fatalf("Reserve: %v, %v", d, err)
		}
	}

	moved, err := q.RecoverProcessing(ctx, key)
	if err != nil {
		t.Fatalf("RecoverProcessing: %v", err)
	}
	if moved != 2 {
		t.Fatalf("moved = %d, want 2", moved)
	}
	if n, _ := q.ProcessingLen(ctx, key); n != 0 {
		t.Fatalf("processing len = %d after replay, want 0", n)
	}
	for _, want := range []string{"a", "b", "c"} {
		d, err := q.Reserve(ctx, key)
		if err != nil || d == nil {
			t.Fatalf("Reserve: %v, %v", d, err)
		}
		if got := d.Items[0].BodyBase64; got != want {
			t.Fatalf("next entry = %q, want %q", got, want)
		}
		if err := q.Ack(ctx, d); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
}

// An entry that does not decode is removed from the processing list, so a
// restart does not replay it forever.
func TestReserveDropsAnUndecodableEntry(t *testing.T) {
	t.Parallel()
	q, mr := newTestQueue(t)
	ctx := context.Background()
	key := ProcessingKey(DefaultWriter)

	if _, err := mr.Lpush(WriteQueueKey, "not json"); err != nil {
		t.Fatalf("lpush: %v", err)
	}
	if _, err := q.Reserve(ctx, key); err == nil {
		t.Fatal("Reserve of an undecodable entry returned no error")
	}
	if n, _ := q.ProcessingLen(ctx, key); n != 0 {
		t.Fatalf("processing len = %d, want 0", n)
	}
}
