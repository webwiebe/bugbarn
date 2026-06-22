package ingestproc

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/wiebe-xyz/bugbarn/internal/logparse"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// replayBatchSize bounds how many held records are read per round so a large
// backlog drains in steady chunks rather than one huge query.
const replayBatchSize = 500

// Replayer drains a project's held-events backlog through the normal persist
// pipeline. It runs on the writer when a pending project is approved. Each
// record is deleted only after it persists, so an interrupted drain is safe to
// resume — replay is at-least-once and PersistProcessedEvent is idempotent on
// fingerprint.
type Replayer struct {
	store  *storage.Store
	proc   *Processor
	logs   LogInserter
	logger *slog.Logger
}

// NewReplayer wires a replayer against the writer store, persist pipeline, and
// log inserter (logs may be nil; held log records are then skipped).
func NewReplayer(store *storage.Store, proc *Processor, logs LogInserter, logger *slog.Logger) *Replayer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Replayer{store: store, proc: proc, logs: logs, logger: logger.With("component", "held-replayer")}
}

// ReplayHeld persists and clears every held record for a project, returning how
// many were replayed. The project must already be active so records are not
// re-held. It stops and returns the error on the first transient failure, having
// already cleared the records it persisted, so a retry resumes from the rest.
func (r *Replayer) ReplayHeld(ctx context.Context, projectID int64) (int, error) {
	total := 0
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		recs, err := r.store.ListHeldByProject(ctx, projectID, replayBatchSize)
		if err != nil {
			return total, fmt.Errorf("list held events: %w", err)
		}
		if len(recs) == 0 {
			if total > 0 {
				r.logger.Info("drained held events for approved project", "project_id", projectID, "replayed", total)
			}
			return total, nil
		}
		for _, rec := range recs {
			if err := r.replayOne(ctx, rec); err != nil {
				r.logger.Error("replay held record", "id", rec.ID, "project_id", projectID, "kind", rec.Kind, "error", err)
				return total, err
			}
			if err := r.store.DeleteHeldEvent(ctx, rec.ID); err != nil {
				return total, fmt.Errorf("delete held event: %w", err)
			}
			total++
		}
	}
}

// replayOne persists a single held record. A permanent failure (unparseable
// payload) is logged and dropped so it never blocks the drain; a transient
// failure is returned so the caller keeps the record for a later retry.
func (r *Replayer) replayOne(ctx context.Context, rec storage.HeldRecord) error {
	switch rec.Kind {
	case storage.HeldKindEvent:
		record := spool.Record{
			IngestID:    rec.IngestID,
			ReceivedAt:  rec.ReceivedAt,
			ContentType: rec.ContentType,
			BodyBase64:  rec.BodyBase64,
			ProjectSlug: rec.Slug,
		}
		res := r.proc.PersistRecord(ctx, record)
		switch res.Outcome {
		case OutcomeSuccess, OutcomeHeld:
			return nil
		case OutcomeParseError:
			r.logger.Error("drop unparseable held event", "id", rec.ID, "error", res.Err)
			return nil
		default:
			return res.Err
		}
	case storage.HeldKindLog:
		if r.logs == nil {
			r.logger.Warn("skipping held log: no log inserter", "id", rec.ID)
			return nil
		}
		body, err := base64.StdEncoding.DecodeString(rec.BodyBase64)
		if err != nil {
			r.logger.Error("drop undecodable held log", "id", rec.ID, "error", err)
			return nil
		}
		entries := logparse.ParseBody(body, rec.ContentType, rec.ProjectID)
		if len(entries) == 0 {
			return nil
		}
		return r.logs.Insert(ctx, entries)
	default:
		r.logger.Warn("dropping held record of unknown kind", "id", rec.ID, "kind", rec.Kind)
		return nil
	}
}
