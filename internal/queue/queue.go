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
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// publishCounter and consumeCounter are the write-queue producer/consumer
// instruments, built once from the global meter (see tracing.Meter) rather
// than recreated per call.
var (
	publishCounter metric.Int64Counter
	consumeCounter metric.Int64Counter
)

func init() {
	m := tracing.Meter()
	publishCounter, _ = m.Int64Counter(
		"bugbarn.queue.publish",
		metric.WithDescription("Write-queue Publish calls, by outcome."),
		metric.WithUnit("{call}"),
	)
	consumeCounter, _ = m.Int64Counter(
		"bugbarn.queue.consume",
		metric.WithDescription("Write-queue Consume calls, by outcome."),
		metric.WithUnit("{call}"),
	)
}

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

// NewRedisQueueLazy builds a queue client without verifying connectivity; the
// connection is established on first use. Use where startup must not block or
// fail on Redis being briefly unavailable — e.g. reader producers, whose local
// spool buffers ingest until the drain can reach Redis.
func NewRedisQueueLazy(redisURL string) (*RedisQueue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis url: %w", err)
	}
	return &RedisQueue{client: redis.NewClient(opts)}, nil
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
	ctx, span := tracing.Tracer().Start(ctx, "queue.Publish",
		trace.WithAttributes(
			attribute.String("queue.name", WriteQueueKey),
			attribute.Int("queue.item_count", len(items)),
		),
	)
	defer span.End()

	batchCount := 0
	for i := 0; i < len(items); i += maxItemsPerBatch {
		end := i + maxItemsPerBatch
		if end > len(items) {
			end = len(items)
		}
		data, err := json.Marshal(items[i:end])
		if err != nil {
			err = fmt.Errorf("queue: marshal: %w", err)
			span.SetStatus(codes.Error, err.Error())
			slog.ErrorContext(ctx, "queue: publish marshal failed", "queue", WriteQueueKey, "error", err)
			publishCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
			return err
		}
		if err := q.client.LPush(ctx, WriteQueueKey, data).Err(); err != nil {
			err = fmt.Errorf("queue: lpush: %w", err)
			span.SetStatus(codes.Error, err.Error())
			slog.ErrorContext(ctx, "queue: publish lpush failed", "queue", WriteQueueKey, "error", err)
			publishCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
			return err
		}
		batchCount++
	}
	span.SetAttributes(attribute.Int("queue.batch_count", batchCount))
	publishCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "success")))
	return nil
}

// Consume blocks for up to brpopTimeout waiting for a batch, then returns it.
// Returns (nil, nil) when the timeout expires with no items — callers loop.
func (q *RedisQueue) Consume(ctx context.Context) ([]Item, error) {
	ctx, span := tracing.Tracer().Start(ctx, "queue.Consume",
		trace.WithAttributes(attribute.String("queue.name", WriteQueueKey)),
	)
	defer span.End()

	result, err := q.client.BRPop(ctx, brpopTimeout, WriteQueueKey).Result()
	if err == redis.Nil {
		// Timeout with no items is the expected idle case, not a failure.
		span.SetAttributes(attribute.Int("queue.item_count", 0))
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "idle-timeout")))
		return nil, nil
	}
	if err != nil {
		err = fmt.Errorf("queue: brpop: %w", err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "queue: consume brpop failed", "queue", WriteQueueKey, "error", err)
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
		return nil, err
	}
	// result[0] = key, result[1] = JSON payload.
	var items []Item
	if err := json.Unmarshal([]byte(result[1]), &items); err != nil {
		err = fmt.Errorf("queue: unmarshal: %w", err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "queue: consume unmarshal failed", "queue", WriteQueueKey, "error", err)
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
		return nil, err
	}
	span.SetAttributes(attribute.Int("queue.item_count", len(items)))
	consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "success")))
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
