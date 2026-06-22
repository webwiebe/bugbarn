// Package ingestproc holds the writer-side persistence pipeline shared by the
// Redis write-queue consumer (spec 007). It is a separate package because
// storage imports worker, so the pipeline (which needs both) cannot live in
// worker. The legacy file-spool worker in cmd/bugbarn still has its own inline
// copy of this pipeline; the two converge in spec 007 phase 5.
package ingestproc

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/service"
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
	// OutcomeParseError: the body could not be parsed — permanent, do not retry.
	OutcomeParseError
	// OutcomeTransient: a retryable persist failure (locked DB, deadline).
	OutcomeTransient
	// OutcomePersistError: a non-retryable persist failure — dead-letter.
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
}

// Processor runs the full persist pipeline for one ingest record:
// parse → resolve project → symbolicate → persist → publish domain event.
// It owns no retry/cursor/dead-letter policy — callers do.
type Processor struct {
	store     *storage.Store
	publisher *service.EventPublisher
	logger    *slog.Logger
	// autoApprove controls how a not-yet-existing project is created on first
	// ingest: active (true) or pending admin approval (false). When pending, the
	// payload is held instead of persisted.
	autoApprove bool
}

// NewProcessor wires the pipeline against a writer store and event publisher.
// When autoApprove is false, brand-new projects are created pending and their
// ingest is held until approved.
func NewProcessor(store *storage.Store, publisher *service.EventPublisher, logger *slog.Logger, autoApprove bool) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{store: store, publisher: publisher, logger: logger.With("component", "ingestproc"), autoApprove: autoApprove}
}

// PersistRecord parses and persists a single ingest record. Mirrors the inline
// pipeline in cmd/bugbarn's runBackgroundWorker so behavior is identical.
func (p *Processor) PersistRecord(ctx context.Context, record spool.Record) Result {
	processed, err := worker.ProcessRecord(record)
	if err != nil {
		return Result{Outcome: OutcomeParseError, Err: err}
	}

	// Resolve the project from the slug stored on the record. A failure here is
	// non-fatal: we fall through with no project context (default project).
	persistCtx := ctx
	if record.ProjectSlug != "" {
		if proj, ok := p.EnsureProjectForIngest(ctx, record.ProjectSlug); ok {
			// Project pending admin approval: park the raw payload and stop. It
			// replays through this same path once the project is approved.
			if proj.Status == "pending" {
				held := storage.HeldRecord{
					ProjectID:   proj.ID,
					Slug:        record.ProjectSlug,
					Kind:        storage.HeldKindEvent,
					IngestID:    record.IngestID,
					ReceivedAt:  record.ReceivedAt,
					ContentType: record.ContentType,
					BodyBase64:  record.BodyBase64,
				}
				if herr := p.Hold(ctx, held); herr != nil {
					return Result{Outcome: OutcomeTransient, Err: herr}
				}
				return Result{Outcome: OutcomeHeld, ProjectID: proj.ID}
			}
			persistCtx = storage.WithProjectID(ctx, proj.ID)
		}
	}

	// Annotate JS stack frames with original positions from stored source maps.
	processed.Event = worker.SymbolicateEvent(persistCtx, processed.Event, p.store)

	issue, _, isNew, isRegressed, perr := p.store.PersistProcessedEvent(persistCtx, processed)
	if perr != nil {
		if isTransientPersistError(perr) {
			return Result{Outcome: OutcomeTransient, Err: perr}
		}
		return Result{Outcome: OutcomePersistError, Err: perr}
	}

	var projectID int64
	if pid, ok := storage.ProjectIDFromContext(persistCtx); ok {
		projectID = pid
	}
	p.publisher.PublishIssueEvent(issue, projectID, isNew, isRegressed)

	return Result{
		Outcome:   OutcomeSuccess,
		Issue:     issue,
		IsNew:     isNew,
		Regressed: isRegressed,
		ProjectID: projectID,
	}
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

// isTransientPersistError mirrors cmd/bugbarn.isTransientPersistError: a locked
// database or a deadline is worth retrying; anything else is permanent.
func isTransientPersistError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return storage.IsDatabaseLocked(err)
}
