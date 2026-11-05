package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// DefaultWriter names the processing list of the one writer. The name must
// stay the same across restarts of that writer: the next start replays what
// the previous process left in the list (bugbarn#257).
const DefaultWriter = "writer"

// ProcessingKey is the Redis list that holds the entries a writer has taken
// off the write queue and not yet acked.
func ProcessingKey(writer string) string {
	return WriteQueueKey + ":processing:" + writer
}

// Delivery is one write-queue entry that a writer holds in its processing
// list until Ack removes it.
type Delivery struct {
	Items         []Item
	payload       string
	processingKey string
}

// Reserve blocks for up to brpopTimeout and moves the oldest write-queue entry
// into the processing list in one step (BLMOVE), then returns it. Returns
// (nil, nil) when the timeout expires with no entry.
//
// A reply that never reaches the writer leaves the entry in the processing
// list, where RecoverProcessing finds it; with BRPOP that entry was gone.
// Like Consume, it does not log a failure; the caller logs it once.
func (q *RedisQueue) Reserve(ctx context.Context, processingKey string) (*Delivery, error) {
	ctx, span := tracing.Tracer().Start(ctx, "queue.Reserve",
		trace.WithAttributes(attribute.String("queue.name", WriteQueueKey)),
	)
	defer span.End()

	payload, err := q.client.BLMove(ctx, WriteQueueKey, processingKey, "RIGHT", "LEFT", brpopTimeout).Result()
	if errors.Is(err, redis.Nil) {
		span.SetAttributes(attribute.Int("queue.item_count", 0))
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "idle-timeout")))
		return nil, nil
	}
	if err != nil {
		err = fmt.Errorf("queue: blmove: %w", err)
		span.SetStatus(codes.Error, err.Error())
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
		return nil, err
	}
	d := &Delivery{payload: payload, processingKey: processingKey}
	if err := json.Unmarshal([]byte(payload), &d.Items); err != nil {
		// An entry that does not decode never will. Remove it so it is not
		// replayed on every start.
		err = fmt.Errorf("queue: unmarshal: %w", err)
		if ackErr := q.Ack(ctx, d); ackErr != nil {
			err = errors.Join(err, ackErr)
		}
		span.SetStatus(codes.Error, err.Error())
		consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "error")))
		return nil, err
	}
	span.SetAttributes(attribute.Int("queue.item_count", len(d.Items)))
	consumeCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "success")))
	return d, nil
}

// Ack removes a delivered entry from the processing list. Call it once the
// entry's items have a final disposition (stored, parked or dropped), or once
// an unfinished tail is back on the write queue.
func (q *RedisQueue) Ack(ctx context.Context, d *Delivery) error {
	if err := q.client.LRem(ctx, d.processingKey, 1, d.payload).Err(); err != nil {
		return fmt.Errorf("queue: lrem: %w", err)
	}
	return nil
}

// RecoverProcessing moves every entry left in the processing list back to the
// consuming end of the write queue, oldest first, and returns how many moved.
// The writer calls it on start, before Reserve: anything still in its list
// belongs to a previous process that stopped before acking.
func (q *RedisQueue) RecoverProcessing(ctx context.Context, processingKey string) (int, error) {
	moved := 0
	for {
		// The processing list holds the newest entry on the left. Taking from
		// the left and pushing on the consuming (right) end leaves the oldest
		// entry rightmost, so it is the next one Reserve takes.
		err := q.client.LMove(ctx, processingKey, WriteQueueKey, "LEFT", "RIGHT").Err()
		if errors.Is(err, redis.Nil) {
			return moved, nil
		}
		if err != nil {
			return moved, fmt.Errorf("queue: lmove: %w", err)
		}
		moved++
	}
}

// ProcessingLen returns the number of entries in a processing list.
func (q *RedisQueue) ProcessingLen(ctx context.Context, processingKey string) (int64, error) {
	n, err := q.client.LLen(ctx, processingKey).Result()
	if err != nil {
		return 0, fmt.Errorf("queue: llen: %w", err)
	}
	return n, nil
}
