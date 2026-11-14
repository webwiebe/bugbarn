package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	bb "github.com/wiebe-xyz/bugbarn-go"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

const (
	workerMaxRetries      = 3
	workerRotateThreshold = 64 << 20 // 64 MiB
)

// PersistOutcome classifies what the persist pipeline did with one record. The
// spool worker applies its delivery policy to it. It mirrors the pipeline's own
// outcomes because the pipeline (internal/ingestproc) depends on storage, which
// depends on this package, so the worker cannot import it.
type PersistOutcome int

const (
	// OutcomeSuccess: the event was persisted.
	OutcomeSuccess PersistOutcome = iota
	// OutcomeParseError: the body could not be parsed; permanent.
	OutcomeParseError
	// OutcomeTransient: a retryable failure (locked database, deadline).
	OutcomeTransient
	// OutcomePersistError: a non-retryable persist failure.
	OutcomePersistError
	// OutcomeHeld: the project is pending approval; the payload is parked.
	OutcomeHeld
)

// PersistResult is what a RecordPersister reports for one event record.
type PersistResult struct {
	Outcome     PersistOutcome
	Err         error
	IsNew       bool
	Regressed   bool
	Fingerprint string
	Severity    string
}

// RecordPersister is the persistence pipeline the spool worker drives.
// ingestproc.Processor is adapted to it in cmd/bugbarn.
type RecordPersister interface {
	PersistRecord(ctx context.Context, record spool.Record) PersistResult
	PersistRelease(ctx context.Context, record spool.Record) error
}

// SpoolWorkerConfig wires the background spool worker.
type SpoolWorkerConfig struct {
	// EventSpool is the active spool the worker rotates; nil disables rotation.
	EventSpool *spool.Spool
	// SpoolDir holds the segments, cursor and dead-letter file.
	SpoolDir string
	// Store applies queued admin mutations.
	Store IssueMutator
	// Persister persists event and release records.
	Persister RecordPersister
	// SelfReporting reports dead letters and stalls to BugBarn itself.
	SelfReporting bool
	// Status receives progress and health; nil disables reporting.
	Status *Status
	// Mutations is the durable queue of admin issue mutations.
	Mutations *mutqueue.Queue
}

// spoolWorker carries the shared state for the background ingest loop so the
// per-record processing steps read as small methods instead of one deeply nested
// function. retryCounts and offset are process-lifetime mutable state; the rest
// is configuration wired once at construction.
type spoolWorker struct {
	eventSpool    *spool.Spool
	spoolDir      string
	store         IssueMutator // admin mutations from the mutation queue
	proc          RecordPersister
	selfReporting bool
	ws            *Status
	mq            *mutqueue.Queue
	tracer        trace.Tracer
	metrics       *spoolMetrics

	retryCounts map[string]int // per-ingest-ID failure counts within this process
	pos         spool.Position // spool cursor: segment being read and offset in it
	stallWarned bool
	// rotateThreshold overrides workerRotateThreshold; zero means the default.
	rotateThreshold int64
}

// RunSpoolWorker restores the spool cursor and runs the worker loop until ctx
// ends.
func RunSpoolWorker(ctx context.Context, cfg SpoolWorkerConfig) {
	w := &spoolWorker{
		eventSpool:    cfg.EventSpool,
		spoolDir:      cfg.SpoolDir,
		store:         cfg.Store,
		proc:          cfg.Persister,
		selfReporting: cfg.SelfReporting,
		ws:            cfg.Status,
		mq:            cfg.Mutations,
		tracer:        tracing.Tracer(),
		metrics:       newSpoolMetrics(),
		retryCounts:   make(map[string]int),
	}
	// Restore the cursor from disk so already-handled records are not
	// re-processed, then delete rotated segments the worker is done with.
	w.restorePosition()
	w.run(ctx)
}

func (w *spoolWorker) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick performs one drain/process/report cycle.
func (w *spoolWorker) tick(ctx context.Context) {
	// Drain queued admin mutations (resolve/reopen/mute/unmute) before processing
	// ingest events so user-initiated actions are applied first.
	if err := w.mq.Drain(func(r mutqueue.Record) error {
		return applyMutation(ctx, w.store, r)
	}); err != nil {
		slog.Error("worker failed to drain mutation queue", "error", err)
	}

	entries, err := spool.ReadRecordsFrom(spool.SegmentPath(w.spoolDir, w.pos.Segment), w.pos.Offset)
	if err != nil {
		slog.Error("worker failed to read spool", "segment", w.pos.Segment, "error", err)
		return
	}

	drained := true
	for _, entry := range entries {
		// A record that fails stops the batch; remaining records retry next tick.
		if w.processEntry(ctx, entry) {
			drained = false
			break
		}
	}

	// A rotated segment receives no appends, so one read that was processed to
	// the end means every record in it has been handled.
	if drained && w.pos.Segment != "" {
		w.finishSegment()
	}

	w.reportStatus()

	// Rotate the active segment once it exceeds the threshold. Never while a
	// rotated segment is still being drained: one rotated segment at a time.
	if w.pos.Segment == "" {
		w.rotate()
	}
}

// processEntry handles one spooled record, returning true when the caller should
// stop draining the current batch (a failure to retry on the next tick).
func (w *spoolWorker) processEntry(ctx context.Context, entry spool.RecordAtOffset) (stop bool) {
	record := entry.Record
	ctx, span := w.tracer.Start(ctx, "worker.ProcessRecord",
		trace.WithAttributes(
			attribute.String("ingest_id", record.IngestID),
			attribute.String("project_slug", record.ProjectSlug),
		),
	)

	// Release markers are enqueued by POST /api/v1/releases so the create stays
	// off the request path. Handle them here instead of the event pipeline.
	if record.Kind == ingest.RecordKindRelease {
		return w.processReleaseEntry(ctx, span, entry)
	}
	return w.processEventEntry(ctx, span, entry)
}

func (w *spoolWorker) processReleaseEntry(ctx context.Context, span trace.Span, entry spool.RecordAtOffset) (stop bool) {
	record := entry.Record
	if err := w.proc.PersistRelease(ctx, record); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.End()
		// Release dead-letters are not self-reported (they predate self-reporting).
		w.failRecord(record, entry.EndOffset, "persist release", err, false)
		return true
	}
	span.End()
	w.markProcessed(record, entry.EndOffset)
	return false
}

// processEventEntry persists one event record through the shared ingestproc
// pipeline and applies the spool's delivery policy to the outcome: advance the
// cursor on success or hold, retry a lock failure forever, and count anything
// else toward the dead-letter budget.
func (w *spoolWorker) processEventEntry(ctx context.Context, span trace.Span, entry spool.RecordAtOffset) (stop bool) {
	record := entry.Record
	res := w.proc.PersistRecord(ctx, record)
	if res.Fingerprint != "" {
		span.SetAttributes(
			attribute.String("fingerprint", res.Fingerprint),
			attribute.String("event.severity", res.Severity),
		)
	}

	switch res.Outcome {
	case OutcomeSuccess:
		span.End()
		w.markProcessed(record, entry.EndOffset,
			attribute.Bool("is_new", res.IsNew),
			attribute.Bool("is_regressed", res.Regressed),
		)
		return false
	case OutcomeHeld:
		// The payload is parked for replay on approval; the record is handled.
		span.End()
		w.markHandled(record, entry.EndOffset, "held")
		return false
	case OutcomeTransient:
		span.SetStatus(codes.Error, res.Err.Error())
		span.End()
		// Environmental (lock contention); retry forever, don't burn the budget.
		slog.Warn("worker transient persist failure, will retry", "ingest_id", record.IngestID, "error", res.Err)
		if w.metrics != nil {
			w.metrics.eventsProcessed.Add(ctx, 1, metric.WithAttributes(
				attribute.String("outcome", "transient_error"),
			))
		}
		return true
	case OutcomeParseError:
		span.SetStatus(codes.Error, res.Err.Error())
		span.End()
		w.failRecord(record, entry.EndOffset, "process record", res.Err, true)
		return true
	default:
		span.SetStatus(codes.Error, res.Err.Error())
		span.End()
		w.failRecord(record, entry.EndOffset, "persist record", res.Err, true)
		return true
	}
}

// markProcessed clears retry state and advances the cursor past a record that was
// handled successfully. extraAttrs are attached to the events_processed metric
// alongside outcome=success (e.g. is_new/is_regressed for event records).
//
// Metrics are recorded against context.Background() rather than a per-record
// span context: this keeps the signature (and its test call sites) stable and
// exemplar correlation isn't required for these counters.
func (w *spoolWorker) markProcessed(record spool.Record, endOffset int64, extraAttrs ...attribute.KeyValue) {
	w.markHandled(record, endOffset, "success", extraAttrs...)
}

// markHandled is markProcessed for any final disposition, labeled with the
// outcome the events_processed metric reports.
func (w *spoolWorker) markHandled(record spool.Record, endOffset int64, outcome string, extraAttrs ...attribute.KeyValue) {
	delete(w.retryCounts, record.IngestID)
	w.advanceCursor(endOffset)
	if w.ws != nil {
		w.ws.RecordProcessed(1)
	}
	if w.metrics != nil {
		attrs := append([]attribute.KeyValue{attribute.String("outcome", outcome)}, extraAttrs...)
		w.metrics.eventsProcessed.Add(context.Background(), 1, metric.WithAttributes(attrs...))
	}
}

// advanceCursor persists the new offset in the current segment and records the
// advance.
func (w *spoolWorker) advanceCursor(endOffset int64) {
	w.pos.Offset = endOffset
	if err := spool.WritePosition(w.spoolDir, w.pos); err != nil {
		slog.Error("worker failed to write cursor", "error", err)
	}
	if w.ws != nil {
		w.ws.RecordAdvance()
	}
}

// failRecord records a failure for one record. It increments the retry counter
// and, once the retry budget is exhausted, dead-letters the record and advances
// the cursor past it. report controls whether the dead-letter is surfaced via
// self-reporting and the worker's dead-letter metrics.
func (w *spoolWorker) failRecord(record spool.Record, endOffset int64, stage string, cause error, report bool) {
	// A canceled context means the process is stopping, not that this record is
	// bad. The cursor is not advanced on failure, so the record is retried from
	// the same spool offset after restart. Spending a retry on a shutdown would
	// eventually dead-letter a perfectly good record, and logging it at ERROR
	// self-reports a bug against us on every deploy.
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		slog.Info("worker "+stage+" interrupted by shutdown", "ingest_id", record.IngestID)
		return
	}
	w.retryCounts[record.IngestID]++
	attempt := w.retryCounts[record.IngestID]
	// ERROR means a record was lost, as in the queue consumer: BugBarn reports
	// its own ERROR logs as bugs. An attempt that will be retried is a WARN.
	if attempt < workerMaxRetries {
		slog.Warn("worker failed to "+stage+", will retry", "ingest_id", record.IngestID, "attempt", attempt, "error", cause)
		return
	}

	slog.Error("worker dead-lettering record", "stage", stage, "ingest_id", record.IngestID, "attempts", attempt, "error", cause)
	if dlErr := spool.AppendDeadLetter(w.spoolDir, record); dlErr != nil {
		slog.Error("worker failed to write dead letter", "ingest_id", record.IngestID, "error", dlErr)
	}
	if report {
		if w.selfReporting {
			bb.CaptureMessage(fmt.Sprintf("dead-letter %s: ingest %s: %v", stage, record.IngestID, cause))
		}
		if w.ws != nil {
			w.ws.RecordDeadLetter()
		}
		if w.metrics != nil {
			w.metrics.deadLetters.Add(context.Background(), 1, metric.WithAttributes(attribute.String("stage", stage)))
		}
	}
	delete(w.retryCounts, record.IngestID)
	w.advanceCursor(endOffset)
}

// reportStatus refreshes the pending-record gauge and emits a one-shot stall
// warning when the worker stops advancing.
func (w *spoolWorker) reportStatus() {
	if w.ws == nil {
		return
	}
	w.ws.SetPendingRecords(w.pendingRecords())
	snap := w.ws.Snapshot()
	switch {
	case !snap.Healthy && !w.stallWarned:
		slog.Info("worker stall detected", "pending_records", snap.PendingRecords, "level", snap.Level, "last_advance", snap.LastAdvance)
		if w.selfReporting {
			bb.CaptureMessage("worker stall: records not advancing",
				bb.WithAttributes(map[string]any{
					"pending_records": snap.PendingRecords,
					"level":           string(snap.Level),
				}),
			)
		}
		w.stallWarned = true
	case snap.Healthy:
		w.stallWarned = false
	}
}
