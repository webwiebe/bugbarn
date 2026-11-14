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
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// LogInserter persists parsed log entries. Satisfied by *service/logs.Service.
type LogInserter interface {
	Insert(ctx context.Context, entries []domain.LogEntry) error
}

const (
	// consumerMaxRetries bounds in-memory retries for a transient persist
	// failure before the item is dropped and its entry acked. A persist error
	// that outlasts the retries is a final disposition: replaying it on every
	// start would not fix the database.
	consumerMaxRetries = 5
	// consumerErrBackoff is the pause after a Redis Consume error before retry.
	consumerErrBackoff = time.Second
	// consumerErrorAfter is how many failed queue calls in a row make an outage.
	// A lone failure loses nothing, since Run replays the processing list, so it
	// logs at WARN; BugBarn reports its own ERROR logs as bugs (#276). The
	// failure that reaches this count logs at ERROR, once per outage: Redis has
	// stayed unreachable and ingest is stalled.
	consumerErrorAfter = 3
	// requeueTimeout bounds the shutdown requeue LPUSH and the ack LREM. Short:
	// the process is already stopping, and an entry that misses it is replayed
	// on the next start.
	requeueTimeout = 5 * time.Second
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
	// telemetry handles security and metrics items; nil drops them.
	telemetry TelemetryIngester
	// processingKey is the Redis list holding this writer's unacked entries.
	processingKey string
	// failures counts queue calls that failed in a row. Only Run's goroutine
	// touches it.
	failures int
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

		processingKey: queue.ProcessingKey(queue.DefaultWriter),
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

// Run loops on Reserve until ctx is canceled.
//
// Each entry sits in the writer's processing list from the moment Reserve
// takes it until processBatch acks it, so neither a lost Redis reply nor a
// writer killed mid-batch drops it (bugbarn#257). Before the first Reserve,
// and again after a Reserve error, Run moves whatever the list holds back to
// the write queue: the writer holds no delivery at those points, so any entry
// there was left by a lost reply or a previous process.
func (c *Consumer) Run(ctx context.Context) {
	go c.monitorDepth(ctx)
	replayed := false
	for ctx.Err() == nil {
		if !replayed {
			replayed = c.replay(ctx)
			continue
		}
		replayed = c.consumeOne(ctx)
	}
}

// replay moves the processing list back to the write queue and reports
// whether it did. After a failure it has logged and backed off; the entries
// stay in the list until the next attempt.
func (c *Consumer) replay(ctx context.Context) bool {
	n, err := c.queue.RecoverProcessing(ctx, c.processingKey)
	if err != nil {
		// Logged like a failed read: Redis is unreachable.
		c.consumeFailed(ctx, err)
		return false
	}
	if n > 0 {
		c.logger.Info("replayed unacked write-queue entries", "entries", n)
	}
	return true
}

// consumeOne reserves one entry, stores its items and acks it. It returns
// false after a failed read, so Run replays the processing list before the
// next one: a reply lost on the way leaves its entry there.
func (c *Consumer) consumeOne(ctx context.Context) bool {
	d, err := c.queue.Reserve(ctx, c.processingKey)
	if err != nil {
		c.consumeFailed(ctx, err)
		return false
	}
	c.failures = 0
	if d != nil && c.processBatch(ctx, d.Items) {
		c.ack(ctx, d)
	}
	return true
}

// consumeFailed logs a failed queue call and backs off. A failure caused by
// shutdown is not logged. The first failures of an outage log at WARN, because
// the entry stays in the processing list and replays; the failure that makes
// the outage (consumerErrorAfter in a row) logs once at ERROR.
func (c *Consumer) consumeFailed(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	c.failures++
	if c.failures == consumerErrorAfter {
		c.logger.Error("consume failed repeatedly; the write queue is unreachable", "failures", c.failures, "error", err)
	} else {
		c.logger.Warn("consume failed; retrying", "failures", c.failures, "error", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(consumerErrBackoff):
	}
}

// ack removes a finished entry from the processing list. A failed ack loses
// nothing: the entry replays on the next start and its items are stored a
// second time, the over-count bugbarn#257 accepts.
func (c *Consumer) ack(ctx context.Context, d *queue.Delivery) {
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requeueTimeout)
	defer cancel()
	if err := c.queue.Ack(ackCtx, d); err != nil {
		c.logger.Warn("ack write-queue entry failed; it replays on the next start",
			"items", len(d.Items), "error", err)
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

// processBatch persists a reserved entry's items and reports whether the
// entry may be acked: true once every item has a final disposition, or once a
// shutdown's unfinished tail is back on the write queue. False leaves the
// entry in the processing list for the next start to replay.
func (c *Consumer) processBatch(ctx context.Context, items []queue.Item) bool {
	if c.writeMu != nil {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
	}
	for i, item := range items {
		if ctx.Err() != nil {
			return c.requeue(ctx, items[i:])
		}
		start := time.Now()
		var outcome string
		switch item.Kind {
		case queue.KindEvent:
			outcome = c.persistEvent(ctx, item)
		case queue.KindLog:
			outcome = c.persistLog(ctx, item)
		case queue.KindSecurity, queue.KindMetrics:
			outcome = c.persistTelemetry(ctx, item)
		default:
			c.logger.Warn("dropping unknown queue item kind", "kind", item.Kind)
			outcome = "unknown_kind"
		}
		kind := item.Kind
		if kind == "" {
			kind = "unknown"
		}
		c.metrics.record(ctx, kind, outcome, float64(time.Since(start).Milliseconds()))

		// Shutdown landed mid-item. Whether this one made it decides where the
		// unfinished tail starts.
		if ctx.Err() != nil {
			if handled(outcome) {
				return c.requeue(ctx, items[i+1:])
			}
			return c.requeue(ctx, items[i:])
		}
	}
	return true
}

// resolveProject resolves the item's project, retrying transient failures the
// same way the log insert below does.
//
// These two cases used to collapse into one silent drop of accepted log data
// (BS2-103's sibling, BS2-120), even though only one of them is permanent:
//
//   - no slug at all — unroutable, since no amount of retrying invents a
//     project to attach the logs to;
//   - slug present but resolution failed — a database blip, exactly the class
//     the insert retries. Dropping here while retrying there was simply
//     inconsistent, and it discarded data we had already accepted.
//
// Returns ok=false with the outcome label to report when the item cannot be
// resolved; "resolve_error" is deliberately not a final disposition, so a
// shutdown hands the item back to the queue instead of eating it.
func (c *Consumer) resolveProject(ctx context.Context, item queue.Item) (storage.Project, string, bool) {
	if item.ProjectSlug == "" {
		// Error, not warn: this discards accepted log data, and selflog only
		// self-reports at >= Error — at Warn the loss stays invisible.
		c.logger.Error("dropping log item: no project slug",
			"ingest_id", item.IngestID, "received_at", item.ReceivedAt)
		return storage.Project{}, "dropped", false
	}
	for attempt := 1; attempt <= consumerMaxRetries; attempt++ {
		if proj, ok := c.proc.EnsureProjectForIngest(ctx, item.ProjectSlug); ok {
			return proj, "", true
		}
		if ctx.Err() != nil {
			return storage.Project{}, "transient_drop", false
		}
		if attempt < consumerMaxRetries {
			select {
			case <-ctx.Done():
				return storage.Project{}, "transient_drop", false
			case <-time.After(time.Duration(attempt*attempt) * 100 * time.Millisecond):
			}
		}
	}
	c.logger.Error("dropping log item: project unresolved after retries",
		"project", item.ProjectSlug, "ingest_id", item.IngestID, "attempts", consumerMaxRetries)
	return storage.Project{}, "resolve_error", false
}

// handled reports whether an outcome is a final disposition — persisted,
// parked, or permanently unprocessable — so the item must not be requeued.
// Anything else was simply not finished and is safe to hand back.
func handled(outcome string) bool {
	switch outcome {
	case "success", "held", "parse_error", "decode_error", "dropped", "empty", "unknown_kind":
		return true
	}
	return false
}

// requeue puts items that shutdown interrupted back on the write queue and
// reports whether it did, so the caller may ack their entry.
//
// Requeueing only the unfinished tail keeps the items already stored in this
// batch from being stored again. When the LPUSH fails, the whole entry stays
// in the processing list and the next start replays it: nothing is lost, and
// the head of the batch is stored a second time (bugbarn#257).
//
// Publish LPUSHes while Reserve takes from the other end, so returned items go
// to the far end of the queue rather than being taken again in this shutdown.
func (c *Consumer) requeue(ctx context.Context, items []queue.Item) bool {
	if len(items) == 0 {
		return true
	}
	// ctx is canceled by definition here, so detach from it — the LPUSH needs
	// a live context to run at all.
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requeueTimeout)
	defer cancel()

	if err := c.queue.Publish(pubCtx, items); err != nil {
		c.logger.Warn("requeue on shutdown failed; the entry replays on the next start",
			"count", len(items), "error", err)
		return false
	}
	c.logger.Info("requeued unprocessed items on shutdown", "count", len(items))
	return true
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
		case OutcomeHeld:
			return "held"
		case OutcomeParseError:
			c.logger.Error("drop unparseable event", "ingest_id", item.IngestID, "error", res.Err)
			return "parse_error"
		case OutcomePersistError:
			// On shutdown the caller requeues this item, so it is not dropped
			// and must not be reported as an error — that log is what filed
			// BS2-103 against us on every deploy.
			if ctx.Err() == nil {
				c.logger.Error("drop event after persist error", "ingest_id", item.IngestID, "error", res.Err)
			}
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
	proj, outcome, ok := c.resolveProject(ctx, item)
	if !ok {
		return outcome
	}
	// Project pending admin approval: park the raw log payload for replay on
	// approval instead of inserting it.
	if proj.Status == "pending" {
		held := storage.HeldRecord{
			ProjectID:   proj.ID,
			Slug:        item.ProjectSlug,
			Kind:        storage.HeldKindLog,
			IngestID:    item.IngestID,
			ReceivedAt:  item.ReceivedAt,
			ContentType: item.ContentType,
			BodyBase64:  item.BodyBase64,
		}
		if err := c.proc.Hold(ctx, held); err != nil {
			return "held_error"
		}
		return "held"
	}
	projectID := proj.ID
	entries := logparse.ParseBody(body, item.ContentType, projectID)
	if len(entries) == 0 {
		return "empty"
	}
	return c.insertLogs(ctx, item.ProjectSlug, entries)
}

// insertLogs inserts the entries, retrying a transient failure with a growing
// backoff, and returns the outcome label persistLog reports.
func (c *Consumer) insertLogs(ctx context.Context, slug string, entries []domain.LogEntry) string {
	for attempt := 1; attempt <= consumerMaxRetries; attempt++ {
		err := c.logs.Insert(ctx, entries)
		if err == nil {
			return "success"
		}
		if !storage.IsTransient(err) {
			c.logger.Error("drop logs after insert error", "project", slug, "count", len(entries), "error", err)
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
	c.logger.Error("drop logs after exhausting retries", "project", slug, "count", len(entries))
	return "retry_exhausted"
}
