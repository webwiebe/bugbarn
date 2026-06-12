package ingestproc

import (
	"context"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// LogInserter persists parsed log entries. Satisfied by *service/logs.Service.
type LogInserter interface {
	Insert(ctx context.Context, entries []domain.LogEntry) error
}

const (
	// consumerMaxRetries bounds in-memory retries for a transient persist
	// failure before the item is dropped. Items are already off the Redis list
	// (plain BRPOP), so a drop here is the at-most-once trade-off documented in
	// spec 007; a future BLMOVE-to-processing-list upgrade makes it exactly-once.
	consumerMaxRetries = 5
	// consumerErrBackoff is the pause after a Redis Consume error before retry.
	consumerErrBackoff = time.Second
)

// Consumer drains the Redis write queue and persists each item through the
// shared writer pipeline. One Consumer runs in the writer pod.
type Consumer struct {
	queue   *queue.RedisQueue
	proc    *Processor
	logs    LogInserter
	logger  *slog.Logger
	writeMu *sync.Mutex
	metrics *consumerMetrics
}

// NewConsumer builds a queue consumer. logs may be nil (log items are then
// dropped). writeMu may be nil; when set, the consumer holds it for the
// DB-write phase of each batch so writes never interleave with other writers
// competing for the SQLite write lock.
func NewConsumer(q *queue.RedisQueue, proc *Processor, logs LogInserter, writeMu *sync.Mutex, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	var depth func(context.Context) (int64, error)
	if q != nil {
		depth = q.Len
	}
	return &Consumer{
		queue:   q,
		proc:    proc,
		logs:    logs,
		writeMu: writeMu,
		logger:  logger.With("component", "redis-consumer"),
		metrics: newConsumerMetrics(depth),
	}
}

// Close releases the consumer's telemetry registrations. Safe to call once Run
// has returned.
func (c *Consumer) Close() {
	if c.metrics != nil {
		c.metrics.close()
	}
}

// depthLogInterval is how often the consumer logs a non-empty queue depth, for
// rollout visibility into backlog.
const depthLogInterval = 30 * time.Second

// Run loops on Consume until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	go c.monitorDepth(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		items, err := c.queue.Consume(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Error("consume failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(consumerErrBackoff):
			}
			continue
		}
		if len(items) == 0 {
			continue // BRPOP timed out — loop and re-check ctx.
		}
		c.processBatch(ctx, items)
	}
}

// monitorDepth periodically logs the write-queue depth when it is backed up, so
// operators can see a backlog forming during the spec 007 rollout.
func (c *Consumer) monitorDepth(ctx context.Context) {
	t := time.NewTicker(depthLogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := c.queue.Len(ctx)
			if err == nil && n > 0 {
				c.logger.Info("write queue backlog", "entries", n)
			}
		}
	}
}

func (c *Consumer) processBatch(ctx context.Context, items []queue.Item) {
	if c.writeMu != nil {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		var outcome string
		switch item.Kind {
		case queue.KindEvent:
			outcome = c.persistEvent(ctx, item)
		case queue.KindLog:
			outcome = c.persistLog(ctx, item)
		default:
			c.logger.Warn("dropping unknown queue item kind", "kind", item.Kind)
			outcome = "unknown_kind"
		}
		kind := item.Kind
		if kind == "" {
			kind = "unknown"
		}
		c.metrics.record(ctx, kind, outcome, float64(time.Since(start).Milliseconds()))
	}
}

// persistEvent persists a single event item and returns a short outcome label
// for telemetry (success, parse_error, persist_error, transient_drop,
// decode_error).
func (c *Consumer) persistEvent(ctx context.Context, item queue.Item) string {
	body, err := base64.StdEncoding.DecodeString(item.BodyBase64)
	if err != nil {
		c.logger.Error("decode event body", "ingest_id", item.IngestID, "error", err)
		return "decode_error"
	}
	record := spool.Record{
		IngestID:      item.IngestID,
		ReceivedAt:    item.ReceivedAt,
		ContentType:   item.ContentType,
		ContentLength: int64(len(body)),
		BodyBase64:    item.BodyBase64,
		ProjectSlug:   item.ProjectSlug,
	}

	for attempt := 1; attempt <= consumerMaxRetries; attempt++ {
		res := c.proc.PersistRecord(ctx, record)
		switch res.Outcome {
		case OutcomeSuccess:
			return "success"
		case OutcomeParseError:
			c.logger.Error("drop unparseable event", "ingest_id", item.IngestID, "error", res.Err)
			return "parse_error"
		case OutcomePersistError:
			c.logger.Error("drop event after persist error", "ingest_id", item.IngestID, "error", res.Err)
			return "persist_error"
		case OutcomeTransient:
			if ctx.Err() != nil {
				return "transient_drop"
			}
			backoff := time.Duration(attempt*attempt) * 100 * time.Millisecond
			c.logger.Info("transient persist failure, retrying", "ingest_id", item.IngestID, "attempt", attempt, "error", res.Err)
			select {
			case <-ctx.Done():
				return "transient_drop"
			case <-time.After(backoff):
			}
		}
	}
	c.logger.Error("drop event after exhausting retries", "ingest_id", item.IngestID)
	return "retry_exhausted"
}

// persistLog persists a single log item and returns a short outcome label for
// telemetry (success, dropped, empty, decode_error, insert_error,
// retry_exhausted).
func (c *Consumer) persistLog(ctx context.Context, item queue.Item) string {
	if c.logs == nil {
		c.logger.Warn("dropping log item: no log inserter configured", "project", item.ProjectSlug)
		return "dropped"
	}
	body, err := base64.StdEncoding.DecodeString(item.BodyBase64)
	if err != nil {
		c.logger.Error("decode log body", "project", item.ProjectSlug, "error", err)
		return "decode_error"
	}
	projectID := c.proc.ResolveProjectID(ctx, item.ProjectSlug)
	if projectID == 0 {
		c.logger.Warn("dropping log item: unresolved project", "project", item.ProjectSlug)
		return "dropped"
	}
	entries := logparse.ParseBody(body, item.ContentType, projectID)
	if len(entries) == 0 {
		return "empty"
	}
	for attempt := 1; attempt <= consumerMaxRetries; attempt++ {
		err := c.logs.Insert(ctx, entries)
		if err == nil {
			return "success"
		}
		if !isTransientPersistError(err) {
			c.logger.Error("drop logs after insert error", "project", item.ProjectSlug, "count", len(entries), "error", err)
			return "insert_error"
		}
		if ctx.Err() != nil {
			return "transient_drop"
		}
		backoff := time.Duration(attempt*attempt) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return "transient_drop"
		case <-time.After(backoff):
		}
	}
	c.logger.Error("drop logs after exhausting retries", "project", item.ProjectSlug, "count", len(entries))
	return "retry_exhausted"
}
