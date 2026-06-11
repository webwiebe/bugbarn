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
}

// NewConsumer builds a queue consumer. logs may be nil (log items are then
// dropped). writeMu may be nil; when set, the consumer holds it for the
// DB-write phase of each batch so writes never interleave with other writers
// competing for the SQLite write lock.
func NewConsumer(q *queue.RedisQueue, proc *Processor, logs LogInserter, writeMu *sync.Mutex, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{queue: q, proc: proc, logs: logs, writeMu: writeMu, logger: logger.With("component", "redis-consumer")}
}

// Run loops on Consume until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
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

func (c *Consumer) processBatch(ctx context.Context, items []queue.Item) {
	if c.writeMu != nil {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		switch item.Kind {
		case queue.KindEvent:
			c.persistEvent(ctx, item)
		case queue.KindLog:
			c.persistLog(ctx, item)
		default:
			c.logger.Warn("dropping unknown queue item kind", "kind", item.Kind)
		}
	}
}

func (c *Consumer) persistEvent(ctx context.Context, item queue.Item) {
	body, err := base64.StdEncoding.DecodeString(item.BodyBase64)
	if err != nil {
		c.logger.Error("decode event body", "ingest_id", item.IngestID, "error", err)
		return
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
			return
		case OutcomeParseError:
			c.logger.Error("drop unparseable event", "ingest_id", item.IngestID, "error", res.Err)
			return
		case OutcomePersistError:
			c.logger.Error("drop event after persist error", "ingest_id", item.IngestID, "error", res.Err)
			return
		case OutcomeTransient:
			if ctx.Err() != nil {
				return
			}
			backoff := time.Duration(attempt*attempt) * 100 * time.Millisecond
			c.logger.Info("transient persist failure, retrying", "ingest_id", item.IngestID, "attempt", attempt, "error", res.Err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
	c.logger.Error("drop event after exhausting retries", "ingest_id", item.IngestID)
}

func (c *Consumer) persistLog(ctx context.Context, item queue.Item) {
	if c.logs == nil {
		c.logger.Warn("dropping log item: no log inserter configured", "project", item.ProjectSlug)
		return
	}
	body, err := base64.StdEncoding.DecodeString(item.BodyBase64)
	if err != nil {
		c.logger.Error("decode log body", "project", item.ProjectSlug, "error", err)
		return
	}
	projectID := c.proc.ResolveProjectID(ctx, item.ProjectSlug)
	if projectID == 0 {
		c.logger.Warn("dropping log item: unresolved project", "project", item.ProjectSlug)
		return
	}
	entries := logparse.ParseBody(body, item.ContentType, projectID)
	if len(entries) == 0 {
		return
	}
	for attempt := 1; attempt <= consumerMaxRetries; attempt++ {
		err := c.logs.Insert(ctx, entries)
		if err == nil {
			return
		}
		if !isTransientPersistError(err) {
			c.logger.Error("drop logs after insert error", "project", item.ProjectSlug, "count", len(entries), "error", err)
			return
		}
		if ctx.Err() != nil {
			return
		}
		backoff := time.Duration(attempt*attempt) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
	c.logger.Error("drop logs after exhausting retries", "project", item.ProjectSlug, "count", len(entries))
}
