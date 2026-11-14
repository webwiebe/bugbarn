package queue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// downQueue returns a queue whose Redis server has stopped, so every command
// fails at once with a connection error.
func downQueue(t *testing.T) *RedisQueue {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	q, err := NewRedisQueue("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisQueue: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	mr.Close()
	return q
}

func opCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCommandsReportAnUnreachableRedis(t *testing.T) {
	t.Parallel()
	q := downQueue(t)
	ctx := opCtx(t)

	if err := q.Publish(ctx, []Item{{Kind: KindEvent}}); err == nil {
		t.Error("Publish must fail")
	}
	if _, err := q.Consume(ctx); err == nil {
		t.Error("Consume must fail")
	}
	if _, err := q.Reserve(ctx, ProcessingKey(DefaultWriter)); err == nil {
		t.Error("Reserve must fail")
	}
	if _, err := q.Len(ctx); err == nil {
		t.Error("Len must fail")
	}
	if _, err := q.ProcessingLen(ctx, ProcessingKey(DefaultWriter)); err == nil {
		t.Error("ProcessingLen must fail")
	}
	if _, err := q.RecoverProcessing(ctx, ProcessingKey(DefaultWriter)); err == nil {
		t.Error("RecoverProcessing must fail")
	}
	if err := q.Ack(ctx, &Delivery{payload: "x", processingKey: ProcessingKey(DefaultWriter)}); err == nil {
		t.Error("Ack must fail")
	}
}

func TestConsumeRejectsAnUndecodableEntry(t *testing.T) {
	t.Parallel()
	q, mr := newTestQueue(t)
	if _, err := mr.Lpush(WriteQueueKey, "not json"); err != nil {
		t.Fatal(err)
	}
	items, err := q.Consume(opCtx(t))
	if err == nil || items != nil {
		t.Fatalf("Consume = %v, %v; want a decode error", items, err)
	}
	if n, _ := q.Len(opCtx(t)); n != 0 {
		t.Fatalf("an entry that never decodes must be gone, queue depth = %d", n)
	}
}

func TestPublishNothingSendsNothing(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueue(t)
	if err := q.Publish(opCtx(t), nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Len(opCtx(t)); n != 0 {
		t.Fatalf("depth = %d, want 0", n)
	}
}

func TestReserveFailureWhenTheQueueKeyHoldsTheWrongType(t *testing.T) {
	t.Parallel()
	q, mr := newTestQueue(t)
	if err := mr.Set(WriteQueueKey, "a string, not a list"); err != nil {
		t.Fatal(err)
	}
	if d, err := q.Reserve(opCtx(t), ProcessingKey(DefaultWriter)); err == nil || d != nil {
		t.Fatalf("Reserve = %v, %v; want a wrong-type error", d, err)
	}
	if items, err := q.Consume(opCtx(t)); err == nil || items != nil {
		t.Fatalf("Consume = %v, %v; want a wrong-type error", items, err)
	}
}

func TestConnectingToAnUnreachableRedisFails(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	addr := mr.Addr()
	mr.Close()
	if _, err := NewRedisQueue("redis://" + addr); err == nil {
		t.Fatal("NewRedisQueue must fail when ping fails")
	}
}

func TestQueueConstructorsRejectABadURL(t *testing.T) {
	t.Parallel()
	if _, err := NewRedisQueueLazy("not-a-url"); err == nil {
		t.Error("NewRedisQueueLazy accepted a bad URL")
	}
	if _, err := NewRedisQueueWithRetry(opCtx(t), "not-a-url"); err == nil {
		t.Error("NewRedisQueueWithRetry accepted a bad URL")
	}
}
