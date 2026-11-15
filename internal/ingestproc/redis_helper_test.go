package ingestproc

import (
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// newTestQueue connects to an in-process miniredis without the startup ping
// and with timeouts long enough that a loaded CI host cannot trip them. The
// production client's 3s ping budget failed TestConsumerDrainsEventQueue on a
// build host at load 200; these tests are about the queue's contents, so a
// slow host only makes them slower.
func newTestQueue(t *testing.T, addr string) *queue.RedisQueue {
	t.Helper()
	q, err := queue.NewRedisQueueLazy("redis://" + addr + "?dial_timeout=30s&read_timeout=30s&write_timeout=30s")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	return q
}
