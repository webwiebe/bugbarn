// Package ingestproc holds the writer-side persistence pipeline. It is the one
// place that turns a decoded ingest record into a stored event: project
// resolution, held payloads for unapproved projects, symbolication, persist,
// and the domain event. Both writers call it, the Redis write-queue consumer
// (spec 007) and the file-spool worker in cmd/bugbarn, and each keeps only its
// own delivery policy (retries, cursor, dead letter, ack). It lives apart from
// internal/worker, which holds the storage-free decode and fingerprint step.
package ingestproc

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// Outcome classifies the result of persisting one ingest record so callers can
// decide whether to retry, drop, or dead-letter it.
type Outcome int

const (
	// OutcomeSuccess: the event was persisted and its domain event published.
	OutcomeSuccess Outcome = iota
	// OutcomeParseError: the body could not be parsed, permanent, do not retry.
	OutcomeParseError
	// OutcomeTransient: a retryable persist failure (locked DB, deadline).
	OutcomeTransient
	// OutcomePersistError: a non-retryable persist failure.
	OutcomePersistError
	// OutcomeHeld: the project is pending approval, so the raw payload was parked
	// in held_events instead of persisted. It will be replayed on approval.
	OutcomeHeld
)

// Result reports the outcome of PersistRecord.
type Result struct {
	Outcome   Outcome
	Err       error
	Issue     storage.Issue
	IsNew     bool
	Regressed bool
	ProjectID int64
	// Fingerprint and Severity come from the decoded event and are set once the
	// record parsed, whatever happened after.
	Fingerprint string
	Severity    string
}

// Span names the persist pipeline emits when a tracer is set. They keep the
// names the spool worker has always used.
const (
	spanResolveProject = "worker.ResolveProject"
	spanSymbolicate    = "worker.Symbolicate"
	spanPersist        = "worker.Persist"
)

// Processor runs the full persist pipeline for one ingest record:
// parse, resolve project, symbolicate, persist, publish domain event.
// It owns no retry, cursor or dead-letter policy; callers do.
type Processor struct {
	store     *storage.Store
	publisher *domainevents.IssuePublisher
	logger    *slog.Logger
	// autoApprove controls how a not-yet-existing project is created on first
	// ingest: active (true) or pending admin approval (false). When pending, the
	// payload is held instead of persisted.
	autoApprove bool
	// tracer, when set, wraps the pipeline steps in spans. Nil emits none.
	tracer trace.Tracer
}

// NewProcessor wires the pipeline against a writer store and event publisher.
// When autoApprove is false, brand-new projects are created pending and their
// ingest is held until approved.
func NewProcessor(store *storage.Store, publisher *domainevents.IssuePublisher, logger *slog.Logger, autoApprove bool) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{store: store, publisher: publisher, logger: logger.With("component", "ingestproc"), autoApprove: autoApprove}
}

// SetTracer makes PersistRecord emit the worker.ResolveProject,
// worker.Symbolicate and worker.Persist spans under the caller's context. The
// spool worker sets it; the queue consumer runs without spans.
func (p *Processor) SetTracer(t trace.Tracer) { p.tracer = t }

// startSpan starts a child span, or returns the context unchanged with a no-op
// span when no tracer is set.
func (p *Processor) startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if p.tracer == nil {
		return ctx, trace.SpanFromContext(context.Background())
	}
	return p.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// PersistRecord parses and persists a single ingest record. Both writers call
// it, so a rule about what happens to a record exists once.
func (p *Processor) PersistRecord(ctx context.Context, record spool.Record) Result {
	processed, err := worker.ProcessRecordCtx(ctx, record)
	if err != nil {
		return Result{Outcome: OutcomeParseError, Err: err}
	}
	res := Result{Fingerprint: processed.Fingerprint, Severity: processed.Event.Severity}

	// Resolve the project from the slug stored on the record. A failure here is
	// non-fatal: we fall through with no project context (default project).
	persistCtx := ctx
	if record.ProjectSlug != "" {
		rctx, rspan := p.startSpan(ctx, spanResolveProject, attribute.String("project_slug", record.ProjectSlug))
		proj, ok := p.EnsureProjectForIngest(rctx, record.ProjectSlug)
		if ok {
			rspan.SetAttributes(attribute.Int64("project_id", proj.ID))
		} else {
			rspan.SetStatus(codes.Error, "project not resolved")
		}
		rspan.End()
		if ok {
			// Project pending admin approval: park the raw payload and stop. It
			// replays through this same path once the project is approved.
			if proj.Status == "pending" {
				return p.holdEvent(ctx, res, proj, record)
			}
			persistCtx = domain.WithProjectID(ctx, proj.ID)
		}
	}

	// Annotate JS stack frames with original positions from stored source maps.
	symCtx, symSpan := p.startSpan(persistCtx, spanSymbolicate)
	processed.Event = worker.SymbolicateEvent(symCtx, processed.Event, p.store)
	symSpan.End()

	pctx, persistSpan := p.startSpan(persistCtx, spanPersist)
	issue, _, isNew, isRegressed, perr := p.store.PersistProcessedEvent(pctx, processed)
	if perr != nil {
		persistSpan.SetStatus(codes.Error, perr.Error())
		persistSpan.End()
		res.Err = perr
		res.Outcome = OutcomePersistError
		if storage.IsTransient(perr) {
			res.Outcome = OutcomeTransient
		}
		return res
	}
	persistSpan.SetAttributes(
		attribute.Bool("is_new", isNew),
		attribute.Bool("is_regressed", isRegressed),
		attribute.String("issue_id", issue.ID),
	)
	persistSpan.End()

	var projectID int64
	if pid, ok := domain.ProjectIDFromContext(persistCtx); ok {
		projectID = pid
	}
	p.publisher.PublishIssueEvent(issue, projectID, isNew, isRegressed)

	res.Outcome = OutcomeSuccess
	res.Issue = issue
	res.IsNew = isNew
	res.Regressed = isRegressed
	res.ProjectID = projectID
	return res
}

// holdEvent parks the record of a pending project. A hold that fails on lock
// contention is transient; any other hold failure is permanent for this record,
// so a caller with an unbounded retry is not stuck on it.
func (p *Processor) holdEvent(ctx context.Context, res Result, proj storage.Project, record spool.Record) Result {
	held := storage.HeldRecord{
		ProjectID:   proj.ID,
		Slug:        record.ProjectSlug,
		Kind:        storage.HeldKindEvent,
		IngestID:    record.IngestID,
		ReceivedAt:  record.ReceivedAt,
		ContentType: record.ContentType,
		BodyBase64:  record.BodyBase64,
	}
	res.ProjectID = proj.ID
	if herr := p.Hold(ctx, held); herr != nil {
		res.Err = herr
		res.Outcome = OutcomePersistError
		if storage.IsTransient(herr) {
			res.Outcome = OutcomeTransient
		}
		return res
	}
	res.Outcome = OutcomeHeld
	return res
}

// EnsureProjectForIngest resolves (creating if needed) the project for slug,
// returning the project and ok=false when slug is empty or the project cannot be
// resolved. A brand-new project is created pending (or active when autoApprove is
// set). Callers must check proj.Status == "pending" and hold the payload rather
// than persist it.
func (p *Processor) EnsureProjectForIngest(ctx context.Context, slug string) (storage.Project, bool) {
	if slug == "" {
		return storage.Project{}, false
	}
	var proj storage.Project
	var err error
	if p.autoApprove {
		proj, err = p.store.EnsureProject(ctx, slug)
	} else {
		proj, err = p.store.EnsureProjectPending(ctx, slug)
	}
	if err != nil {
		p.logger.Error("ensure project", "project_slug", slug, "error", err)
		return storage.Project{}, false
	}
	return proj, true
}

// Hold parks a raw ingest payload for a project pending admin approval. The
// error is logged here (services are the log boundary) and returned so the
// caller can retry.
func (p *Processor) Hold(ctx context.Context, h storage.HeldRecord) error {
	if err := p.store.HoldEvent(ctx, h); err != nil {
		p.logger.Error("hold ingest for pending project", "project_id", h.ProjectID, "kind", h.Kind, "error", err)
		return err
	}
	return nil
}
