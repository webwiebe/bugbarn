package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	bb "github.com/wiebe-xyz/bugbarn-go"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/issues"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func runWorkerOnce(cfg config.Config) error {
	persistentStore, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer persistentStore.Close()

	records, err := spool.ReadRecords(spool.Path(cfg.SpoolDir))
	if err != nil {
		return err
	}

	// Release markers share the spool with events but take a different path.
	eventRecords := make([]spool.Record, 0, len(records))
	releaseCount := 0
	for _, record := range records {
		if record.Kind == ingest.RecordKindRelease {
			if err := persistReleaseRecord(context.Background(), persistentStore, record); err != nil {
				return err
			}
			releaseCount++
			continue
		}
		eventRecords = append(eventRecords, record)
	}

	processed, err := worker.ProcessRecords(eventRecords)
	if err != nil {
		return err
	}

	store := issues.NewStore()
	for _, item := range processed {
		store.AddWithFingerprint(item.Event, item.Fingerprint)
		if _, _, _, _, err := persistentStore.PersistProcessedEvent(context.Background(), item); err != nil {
			return err
		}
	}

	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"records":  len(records),
		"events":   len(processed),
		"releases": releaseCount,
		"issues":   store.Len(),
	})
}

const (
	workerMaxRetries      = 3
	workerRotateThreshold = 64 << 20 // 64 MiB
)

// isTransientPersistError reports whether a persist failure should be retried
// forever instead of counting toward the dead-letter budget. SQLite lock
// contention (SQLITE_BUSY/BUSY_SNAPSHOT, "database is locked") is environmental
// — Litestream checkpointing, slow disk — and resolves on its own.
func isTransientPersistError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// persistReleaseRecord decodes a spooled release marker and creates it. Release
// markers are enqueued by POST /api/v1/releases so the create stays off the
// request path — the worker owns the single SQLite writer connection. The body
// mirrors the JSON the API handler accepted; the resolved project is carried on
// the record so it need not be re-resolved here.
func persistReleaseRecord(ctx context.Context, store *storage.Store, record spool.Record) error {
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil {
		return fmt.Errorf("decode release body: %w", err)
	}
	var req struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		ObservedAt  string `json:"observedAt"`
		Version     string `json:"version"`
		CommitSHA   string `json:"commitSha"`
		URL         string `json:"url"`
		Notes       string `json:"notes"`
		CreatedBy   string `json:"createdBy"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("unmarshal release: %w", err)
	}
	release := domain.Release{
		Name:        req.Name,
		Environment: req.Environment,
		Version:     req.Version,
		CommitSHA:   req.CommitSHA,
		URL:         req.URL,
		Notes:       req.Notes,
		CreatedBy:   req.CreatedBy,
	}
	if parsed, err := time.Parse(time.RFC3339Nano, req.ObservedAt); err == nil {
		release.ObservedAt = parsed
	}
	if record.ProjectID > 0 {
		ctx = storage.WithProjectID(ctx, record.ProjectID)
	}
	_, err = store.CreateRelease(ctx, release)
	return err
}

// spoolWorker carries the shared state for the background ingest loop so the
// per-record processing steps read as small methods instead of one deeply nested
// function. retryCounts and offset are process-lifetime mutable state; the rest
// is configuration wired once at construction.
type spoolWorker struct {
	eventSpool    *spool.Spool
	spoolDir      string
	store         *storage.Store
	svc           *service.EventPublisher
	selfReporting bool
	ws            *worker.Status
	mq            *mutqueue.Queue
	tracer        trace.Tracer

	retryCounts map[string]int // per-ingest-ID failure counts within this process
	offset      int64          // spool cursor
	stallWarned bool
}

func runBackgroundWorker(ctx context.Context, eventSpool *spool.Spool, spoolDir string, store *storage.Store, svc *service.EventPublisher, selfReporting bool, ws *worker.Status, mq *mutqueue.Queue) {
	// Restore cursor position from disk so we never re-process already-handled records.
	offset, err := spool.ReadCursor(spoolDir)
	if err != nil {
		slog.Error("worker failed to read cursor, starting from 0", "error", err)
		offset = 0
	}
	w := &spoolWorker{
		eventSpool:    eventSpool,
		spoolDir:      spoolDir,
		store:         store,
		svc:           svc,
		selfReporting: selfReporting,
		ws:            ws,
		mq:            mq,
		tracer:        tracing.Tracer(),
		retryCounts:   make(map[string]int),
		offset:        offset,
	}
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

	entries, err := spool.ReadRecordsFrom(spool.Path(w.spoolDir), w.offset)
	if err != nil {
		slog.Error("worker failed to read spool", "error", err)
		return
	}

	for _, entry := range entries {
		// A record that fails stops the batch; remaining records retry next tick.
		if w.processEntry(ctx, entry) {
			break
		}
	}

	w.reportStatus()

	// Rotate the active spool file once it exceeds the threshold, so old segments
	// can eventually be archived or deleted.
	if err := w.eventSpool.RotateIfExceeds(workerRotateThreshold); err != nil {
		slog.Error("worker failed to rotate spool", "error", err)
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
	if err := persistReleaseRecord(ctx, w.store, record); err != nil {
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

func (w *spoolWorker) processEventEntry(ctx context.Context, span trace.Span, entry spool.RecordAtOffset) (stop bool) {
	record := entry.Record

	processed, err := worker.ProcessRecord(record)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.End()
		w.failRecord(record, entry.EndOffset, "process record", err, true)
		return true
	}

	span.SetAttributes(
		attribute.String("fingerprint", processed.Fingerprint),
		attribute.String("event.severity", processed.Event.Severity),
	)

	persistCtx := w.resolveProject(ctx, record)

	// Annotate JS stack frames with original positions from stored source maps.
	_, symSpan := w.tracer.Start(persistCtx, "worker.Symbolicate")
	processed.Event = worker.SymbolicateEvent(persistCtx, processed.Event, w.store)
	symSpan.End()

	_, persistSpan := w.tracer.Start(persistCtx, "worker.Persist")
	issue, _, isNew, isRegressed, persistErr := w.store.PersistProcessedEvent(persistCtx, processed)
	if persistErr != nil {
		persistSpan.SetStatus(codes.Error, persistErr.Error())
		persistSpan.End()
		span.SetStatus(codes.Error, persistErr.Error())
		span.End()
		if isTransientPersistError(persistErr) {
			// Environmental (lock contention); retry forever, don't burn the budget.
			slog.Warn("worker transient persist failure, will retry", "ingest_id", record.IngestID, "error", persistErr)
			return true
		}
		w.failRecord(record, entry.EndOffset, "persist record", persistErr, true)
		return true
	}
	persistSpan.SetAttributes(
		attribute.Bool("is_new", isNew),
		attribute.Bool("is_regressed", isRegressed),
		attribute.String("issue_id", issue.ID),
	)
	persistSpan.End()

	// Publish domain events after successful persistence.
	var projectID int64
	if pid, ok := storage.ProjectIDFromContext(persistCtx); ok {
		projectID = pid
	}
	w.svc.PublishIssueEvent(issue, projectID, isNew, isRegressed)

	span.End()
	w.markProcessed(record, entry.EndOffset)
	return false
}

// resolveProject resolves the record's project slug to a project-scoped context,
// falling back to ctx unchanged when there's no slug or resolution fails.
func (w *spoolWorker) resolveProject(ctx context.Context, record spool.Record) context.Context {
	if record.ProjectSlug == "" {
		return ctx
	}
	_, resolveSpan := w.tracer.Start(ctx, "worker.ResolveProject",
		trace.WithAttributes(attribute.String("project_slug", record.ProjectSlug)),
	)
	defer resolveSpan.End()
	proj, err := w.store.EnsureProject(ctx, record.ProjectSlug)
	if err != nil {
		slog.Error("worker failed to ensure project", "project_slug", record.ProjectSlug, "error", err)
		resolveSpan.SetStatus(codes.Error, err.Error())
		return ctx
	}
	resolveSpan.SetAttributes(attribute.Int64("project_id", proj.ID))
	return storage.WithProjectID(ctx, proj.ID)
}

// markProcessed clears retry state and advances the cursor past a record that was
// handled successfully.
func (w *spoolWorker) markProcessed(record spool.Record, endOffset int64) {
	delete(w.retryCounts, record.IngestID)
	w.advanceCursor(endOffset)
	if w.ws != nil {
		w.ws.RecordProcessed(1)
	}
}

// advanceCursor persists the new spool offset and records the advance.
func (w *spoolWorker) advanceCursor(endOffset int64) {
	w.offset = endOffset
	if err := spool.WriteCursor(w.spoolDir, w.offset); err != nil {
		slog.Error("worker failed to write cursor", "error", err)
	}
	if w.ws != nil {
		w.ws.RecordAdvance()
	}
}

// failRecord records a failure for one record. It increments the retry counter
// and, once the retry budget is exhausted, dead-letters the record and advances
// the cursor past it. report controls whether the dead-letter is surfaced via
// self-reporting and the worker's dead-letter metric.
func (w *spoolWorker) failRecord(record spool.Record, endOffset int64, stage string, cause error, report bool) {
	w.retryCounts[record.IngestID]++
	attempt := w.retryCounts[record.IngestID]
	slog.Error("worker failed to "+stage, "ingest_id", record.IngestID, "attempt", attempt, "error", cause)
	if attempt < workerMaxRetries {
		return
	}

	slog.Error("worker dead-lettering record", "stage", stage, "ingest_id", record.IngestID, "attempts", attempt)
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
	remaining, _ := spool.ReadRecordsFrom(spool.Path(w.spoolDir), w.offset)
	w.ws.SetPendingRecords(int64(len(remaining)))
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

// applyMutation executes a single queued admin mutation against the store.
func applyMutation(ctx context.Context, store *storage.Store, r mutqueue.Record) error {
	switch r.Op {
	case mutqueue.OpResolve:
		_, err := store.ResolveIssue(ctx, r.IssueID)
		return err
	case mutqueue.OpReopen:
		_, err := store.ReopenIssue(ctx, r.IssueID)
		return err
	case mutqueue.OpMute:
		_, err := store.MuteIssue(ctx, r.IssueID, r.MuteMode)
		return err
	case mutqueue.OpUnmute:
		_, err := store.UnmuteIssue(ctx, r.IssueID)
		return err
	default:
		slog.Warn("mutqueue: unknown op, skipping", "op", r.Op, "issue_id", r.IssueID)
		return nil
	}
}
