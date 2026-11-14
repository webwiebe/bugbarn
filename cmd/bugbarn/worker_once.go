package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// replaySummary is what worker-once reports after one pass over the spool.
type replaySummary struct {
	Records  int `json:"records"`
	Events   int `json:"events"`
	Releases int `json:"releases"`
	Held     int `json:"held"`
	Issues   int `json:"issues"`
}

// runWorkerOnce drains the pending spool once and exits. It reads from the
// worker's cursor, advances the cursor after each record and deletes a rotated
// segment it finishes, exactly as the background worker does, so a second run
// finds nothing and the running server never replays what this run handled.
// Run it with the server stopped: both write the same cursor.
func runWorkerOnce(cfg config.Config) error {
	persistentStore, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer persistentStore.Close()
	persistentStore.SetSampleAfter(int64(cfg.EventSampleAfter))

	pending, err := spool.ReadPending(cfg.SpoolDir)
	if err != nil {
		return err
	}

	// No alert subscribers: a maintenance replay stores events and sends no mail.
	publisher := domainevents.NewIssuePublisher(&domainevents.Bus{})
	proc := ingestproc.NewProcessor(persistentStore, publisher, slog.Default(), cfg.AutoApproveProjects)
	summary, err := replayPending(context.Background(), proc, cfg.SpoolDir, pending)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

// replayPending persists each pending record through the shared pipeline and
// commits the cursor after it. It stops at the first failure with the cursor on
// the last handled record. Release markers share the spool with events but take
// the release path.
func replayPending(ctx context.Context, proc *ingestproc.Processor, dir string, pending []spool.Pending) (replaySummary, error) {
	summary := replaySummary{Records: len(pending)}
	issues := make(map[string]struct{})
	for _, p := range pending {
		if p.Record.Kind == ingest.RecordKindRelease {
			if err := proc.PersistRelease(ctx, p.Record); err != nil {
				return summary, err
			}
			summary.Releases++
		} else {
			res := proc.PersistRecord(ctx, p.Record)
			switch res.Outcome {
			case ingestproc.OutcomeSuccess:
				summary.Events++
				issues[res.Fingerprint] = struct{}{}
			case ingestproc.OutcomeHeld:
				summary.Held++
			default:
				return summary, res.Err
			}
		}
		if err := spool.CommitPosition(dir, p.After); err != nil {
			return summary, err
		}
		if p.Drained != "" {
			if _, err := spool.RemoveSegment(dir, p.Drained); err != nil {
				slog.Warn("worker-once could not delete a drained spool segment", "segment", p.Drained, "error", err)
			}
		}
	}
	summary.Issues = len(issues)
	return summary, nil
}
