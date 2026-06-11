// Package queue implements the BugBarn write queue: a durable Redis list that
// decouples ingest producers (reader pods) from the single SQLite writer.
//
// Producers LPUSH batches of Items and return immediately; the single consumer
// (the writer pod) BRPOPs and drains at its own pace. This replaces the
// reader→writer HTTP forwarding from spec 006, whose timeout-driven retries
// could overwhelm the writer under load. See specs/007-redis-write-queue.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// WriteQueueKey is the Redis list backing the write queue.
const WriteQueueKey = "bugbarn:write-queue"

const (
	// maxItemsPerBatch caps how many Items are packed into a single list entry,
	// bounding the size of any one BRPOP payload.
	maxItemsPerBatch = 500
	// brpopTimeout is how long Consume blocks waiting for an entry before
	// returning (nil, nil) so the caller can re-check its context and loop.
	brpopTimeout = 5 * time.Second
)

// Kind discriminates the payload a queue Item carries.
const (
	KindEvent = "event"
	KindLog   = "log"
)

// Item is a single decoupled write. It mirrors the relevant fields of
// spool.Record plus a Kind discriminator so the consumer can dispatch the raw
// body to the right pipeline (event persistence vs log insert).
type Item struct {
	Kind        string    `json:"kind"`
	IngestID    string    `json:"ingestId,omitempty"`
	ReceivedAt  time.Time `json:"receivedAt"`
	ContentType string    `json:"contentType,omitempty"`
	ProjectSlug string    `json:"projectSlug,omitempty"`
	BodyBase64  string    `json:"bodyBase64"`
}

// RedisQueue is a durable write queue backed by a Redis list.
type RedisQueue struct {
	client *redis.Client
}

// NewRedisQueue connects to Redis at redisURL and verifies connectivity.
func NewRedisQueue(redisURL string) (*RedisQueue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis url: %w", err)
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("queue: redis ping: %w", err)
	}
	return &RedisQueue{client: client}, nil
}

// NewRedisQueueWithRetry connects to Redis, retrying with capped exponential
// backoff until ctx is cancelled or the connection succeeds. Used in writer
// mode, where the Redis queue pod may start after the writer during a rolling
// deploy.
func NewRedisQueueWithRetry(ctx context.Context, redisURL string) (*RedisQueue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis url: %w", err)
	}

	backoff := time.Second
	for {
		client := redis.NewClient(opts)
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := client.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return &RedisQueue{client: client}, nil
		}
		client.Close()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("queue: context cancelled waiting for redis: %w", ctx.Err())
		case <-time.After(backoff):
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

// Publish serialises items as JSON and LPUSHes them to the queue in batches of
// up to maxItemsPerBatch. Producers call this after a record is durably in the
// local spool; the spool cursor advances only once Publish returns nil.
func (q *RedisQueue) Publish(ctx context.Context, items []Item) error {
	for i := 0; i < len(items); i += maxItemsPerBatch {
		end := i + maxItemsPerBatch
		if end > len(items) {
			end = len(items)
		}
		data, err := json.Marshal(items[i:end])
		if err != nil {
			return fmt.Errorf("queue: marshal: %w", err)
		}
		if err := q.client.LPush(ctx, WriteQueueKey, data).Err(); err != nil {
			return fmt.Errorf("queue: lpush: %w", err)
		}
	}
	return nil
}

// Consume blocks for up to brpopTimeout waiting for a batch, then returns it.
// Returns (nil, nil) when the timeout expires with no items — callers loop.
func (q *RedisQueue) Consume(ctx context.Context) ([]Item, error) {
	result, err := q.client.BRPop(ctx, brpopTimeout, WriteQueueKey).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: brpop: %w", err)
	}
	// result[0] = key, result[1] = JSON payload.
	var items []Item
	if err := json.Unmarshal([]byte(result[1]), &items); err != nil {
		return nil, fmt.Errorf("queue: unmarshal: %w", err)
	}
	return items, nil
}

// Len returns the current depth of the write queue (number of list entries, not
// individual items). Used for health and backpressure metrics.
func (q *RedisQueue) Len(ctx context.Context) (int64, error) {
	n, err := q.client.LLen(ctx, WriteQueueKey).Result()
	if err != nil {
		return 0, fmt.Errorf("queue: llen: %w", err)
	}
	return n, nil
}

// Close releases the underlying Redis connection.
func (q *RedisQueue) Close() error {
	return q.client.Close()
}
