package queue

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestLazyQueueConnectsOnFirstUse(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	q, err := NewRedisQueueLazy("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("lazy: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	if err := q.client.Ping(context.Background()).Err(); err != nil {
		t.Errorf("first use: %v", err)
	}
	if _, err := NewRedisQueueLazy("not a url"); err == nil {
		t.Error("a malformed url must fail at construction")
	}
}

func TestQueueWithRetryConnectsAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	q, err := NewRedisQueueWithRetry(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatalf("with retry: %v", err)
	}
	q.Close()

	if _, err := NewRedisQueueWithRetry(context.Background(), "not a url"); err == nil {
		t.Error("a malformed url must fail without retrying")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRedisQueueWithRetry(ctx, "redis://127.0.0.1:1"); err == nil {
		t.Error("a canceled context must stop the retry loop")
	}
}
