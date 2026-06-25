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

func runBackgroundWorker(ctx context.Context, eventSpool *spool.Spool, spoolDir string, store *storage.Store, svc *service.EventPublisher, selfReporting bool, ws *worker.Status, mq *mutqueue.Queue) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	tracer := tracing.Tracer()

	// Restore cursor position from disk so we never re-process already-handled records.
	offset, err := spool.ReadCursor(spoolDir)
	if err != nil {
		slog.Error("worker failed to read cursor, starting from 0", "error", err)
		offset = 0
	}

	// retryCounts tracks per-ingest-ID failure counts within this process lifetime.
	retryCounts := make(map[string]int)
	var stallWarned bool

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Drain queued admin mutations (resolve/reopen/mute/unmute) before
			// processing ingest events so user-initiated actions are applied first.
			if err := mq.Drain(func(r mutqueue.Record) error {
				return applyMutation(ctx, store, r)
			}); err != nil {
				slog.Error("worker failed to drain mutation queue", "error", err)
			}

			entries, err := spool.ReadRecordsFrom(spool.Path(spoolDir), offset)
			if err != nil {
				slog.Error("worker failed to read spool", "error", err)
				continue
			}

			for _, entry := range entries {
				record := entry.Record

				recordCtx, recordSpan := tracer.Start(ctx, "worker.ProcessRecord",
					trace.WithAttributes(
						attribute.String("ingest_id", record.IngestID),
						attribute.String("project_slug", record.ProjectSlug),
					),
				)

				// Release markers are enqueued by POST /api/v1/releases so the
				// create stays off the request path. Handle them here instead of
				// the event pipeline.
				if record.Kind == ingest.RecordKindRelease {
					if err := persistReleaseRecord(recordCtx, store, record); err != nil {
						recordSpan.SetStatus(codes.Error, err.Error())
						recordSpan.End()
						retryCounts[record.IngestID]++
						slog.Error("worker failed to persist release", "ingest_id", record.IngestID, "attempt", retryCounts[record.IngestID], "error", err)
						if retryCounts[record.IngestID] >= workerMaxRetries {
							slog.Error("worker dead-lettering release record", "ingest_id", record.IngestID, "attempts", retryCounts[record.IngestID])
							if dlErr := spool.AppendDeadLetter(spoolDir, record); dlErr != nil {
								slog.Error("worker failed to write dead letter", "ingest_id", record.IngestID, "error", dlErr)
							}
							delete(retryCounts, record.IngestID)
							offset = entry.EndOffset
							if err := spool.WriteCursor(spoolDir, offset); err != nil {
								slog.Error("worker failed to write cursor", "error", err)
							}
							if ws != nil {
								ws.RecordAdvance()
							}
						}
						// Stop processing this batch; retry remaining records next tick.
						break
					}
					recordSpan.End()
					delete(retryCounts, record.IngestID)
					offset = entry.EndOffset
					if err := spool.WriteCursor(spoolDir, offset); err != nil {
						slog.Error("worker failed to write cursor", "error", err)
					}
					if ws != nil {
						ws.RecordAdvance()
						ws.RecordProcessed(1)
					}
					continue
				}

				processed, err := worker.ProcessRecord(record)
				if err != nil {
					recordSpan.SetStatus(codes.Error, err.Error())
					recordSpan.End()
					retryCounts[record.IngestID]++
					slog.Error("worker failed to process record", "ingest_id", record.IngestID, "attempt", retryCounts[record.IngestID], "error", err)
					if retryCounts[record.IngestID] >= workerMaxRetries {
						slog.Error("worker dead-lettering record", "ingest_id", record.IngestID, "attempts", retryCounts[record.IngestID])
						if dlErr := spool.AppendDeadLetter(spoolDir, record); dlErr != nil {
							slog.Error("worker failed to write dead letter", "ingest_id", record.IngestID, "error", dlErr)
						}
						if selfReporting {
							bb.CaptureMessage(fmt.Sprintf("dead-letter: ingest %s: %v", record.IngestID, err))
						}
						if ws != nil {
							ws.RecordDeadLetter()
						}
						delete(retryCounts, record.IngestID)
						// Advance cursor past this dead-lettered record.
						offset = entry.EndOffset
						if err := spool.WriteCursor(spoolDir, offset); err != nil {
							slog.Error("worker failed to write cursor", "error", err)
						}
						if ws != nil {
							ws.RecordAdvance()
						}
					}
					// Stop processing this batch; retry remaining records next tick.
					break
				}

				recordSpan.SetAttributes(
					attribute.String("fingerprint", processed.Fingerprint),
					attribute.String("event.severity", processed.Event.Severity),
				)

				// Resolve project from the slug stored in the spool record.
				persistCtx := recordCtx
				if record.ProjectSlug != "" {
					_, resolveSpan := tracer.Start(recordCtx, "worker.ResolveProject",
						trace.WithAttributes(attribute.String("project_slug", record.ProjectSlug)),
					)
					if proj, err := store.EnsureProject(recordCtx, record.ProjectSlug); err == nil {
						persistCtx = storage.WithProjectID(recordCtx, proj.ID)
						resolveSpan.SetAttributes(attribute.Int64("project_id", proj.ID))
					} else {
						slog.Error("worker failed to ensure project", "project_slug", record.ProjectSlug, "error", err)
						resolveSpan.SetStatus(codes.Error, err.Error())
					}
					resolveSpan.End()
				}

				// Annotate JS stack frames with original positions from stored source maps.
				_, symSpan := tracer.Start(persistCtx, "worker.Symbolicate")
				processed.Event = worker.SymbolicateEvent(persistCtx, processed.Event, store)
				symSpan.End()

				_, persistSpan := tracer.Start(persistCtx, "worker.Persist")
				issue, _, isNew, isRegressed, persistErr := store.PersistProcessedEvent(persistCtx, processed)
				if persistErr != nil {
					persistSpan.SetStatus(codes.Error, persistErr.Error())
					persistSpan.End()
					recordSpan.SetStatus(codes.Error, persistErr.Error())
					recordSpan.End()
					if isTransientPersistError(persistErr) {
						slog.Warn("worker transient persist failure, will retry", "ingest_id", record.IngestID, "error", persistErr)
						break
					}
					retryCounts[record.IngestID]++
					slog.Error("worker failed to persist record", "ingest_id", record.IngestID, "attempt", retryCounts[record.IngestID], "error", persistErr)
					if retryCounts[record.IngestID] >= workerMaxRetries {
						slog.Error("worker dead-lettering record after persist failures", "ingest_id", record.IngestID, "attempts", retryCounts[record.IngestID])
						if dlErr := spool.AppendDeadLetter(spoolDir, record); dlErr != nil {
							slog.Error("worker failed to write dead letter", "ingest_id", record.IngestID, "error", dlErr)
						}
						if selfReporting {
							bb.CaptureMessage(fmt.Sprintf("dead-letter persist: ingest %s: %v", record.IngestID, persistErr))
						}
						if ws != nil {
							ws.RecordDeadLetter()
						}
						delete(retryCounts, record.IngestID)
						// Advance cursor past this dead-lettered record.
						offset = entry.EndOffset
						if err := spool.WriteCursor(spoolDir, offset); err != nil {
							slog.Error("worker failed to write cursor", "error", err)
						}
						if ws != nil {
							ws.RecordAdvance()
						}
					}
					// Stop processing this batch; retry remaining records next tick.
					break
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
				svc.PublishIssueEvent(issue, projectID, isNew, isRegressed)

				recordSpan.End()

				delete(retryCounts, record.IngestID)
				// Advance cursor after each successfully processed record.
				offset = entry.EndOffset
				if err := spool.WriteCursor(spoolDir, offset); err != nil {
					slog.Error("worker failed to write cursor", "error", err)
				}
				if ws != nil {
					ws.RecordAdvance()
					ws.RecordProcessed(1)
				}
			}

			if ws != nil {
				remaining, _ := spool.ReadRecordsFrom(spool.Path(spoolDir), offset)
				ws.SetPendingRecords(int64(len(remaining)))
				snap := ws.Snapshot()
				if !snap.Healthy && !stallWarned {
					slog.Info("worker stall detected", "pending_records", snap.PendingRecords, "level", snap.Level, "last_advance", snap.LastAdvance)
					if selfReporting {
						bb.CaptureMessage("worker stall: records not advancing",
							bb.WithAttributes(map[string]any{
								"pending_records": snap.PendingRecords,
								"level":           string(snap.Level),
							}),
						)
					}
					stallWarned = true
				} else if snap.Healthy {
					stallWarned = false
				}
			}

			// Rotate the active spool file once it exceeds the threshold, so old
			// segments can eventually be archived or deleted.
			if err := eventSpool.RotateIfExceeds(workerRotateThreshold); err != nil {
				slog.Error("worker failed to rotate spool", "error", err)
			}
		}
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
