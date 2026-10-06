package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingesthealth"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func startIngestHealthMonitor(ctx context.Context, cfg config.Config, store *storage.Store, apiServer *api.Server, logger *slog.Logger, wg *sync.WaitGroup) {
	deps := ingesthealth.Deps{
		LastEventAt: store.LastEventReceivedAt,
		DBPath:      cfg.DBPath,
	}
	if cfg.RedisQueueURL != "" {
		if q, err := queue.NewRedisQueueLazy(cfg.RedisQueueURL); err == nil {
			deps.QueueDepth = q.Len
		} else {
			logger.Warn("ingest-health: write-queue depth unavailable", "error", err)
		}
	}
	deps.SpoolBacklog = spoolBacklogSource(cfg, logger)
	monitor := ingesthealth.New(ingesthealth.Config{
		Environment: cfg.Environment,
		StaleAfter:  cfg.IngestStaleAfter,
	}, deps, logger)
	// Out-of-band channels: an alert about ingest being broken cannot be
	// delivered through ingest (see the ingesthealth package doc). Unconfigured
	// channels construct to nil and are skipped.
	monitor.AddNotifier(
		ingesthealth.NewWebhookNotifier(cfg.IngestAlertWebhookURL),
		ingesthealth.NewEmailNotifier(cfg.Digest.Mail, cfg.IngestAlertEmail),
	)
	apiServer.SetIngestHealth(monitor.Snapshot)
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		monitor.Start(ctx)
	}()
}

// spoolBacklogSource returns the reader of the writer spool's backlog for the
// ingest-health monitor, or nil when this process cannot see that spool. The
// writer and the monolith read their own spool; a reader reads the writer's
// through BUGBARN_WRITER_SPOOL_DIR on the shared volume, the same way it asks
// Redis for the queue depth. A path that cannot be read at startup is skipped
// with a warning, so a wrong path does not log an error every sample.
func spoolBacklogSource(cfg config.Config, logger *slog.Logger) func(context.Context) (ingesthealth.SpoolBacklog, error) {
	dir := cfg.SpoolDir
	if cfg.Mode == "reader" {
		dir = cfg.WriterSpoolDir
	}
	if dir == "" {
		return nil
	}
	if _, err := spool.ReadBacklog(dir); err != nil {
		logger.Warn("ingest-health: spool backlog unavailable", "dir", dir, "error", err)
		return nil
	}
	return func(context.Context) (ingesthealth.SpoolBacklog, error) {
		b, err := spool.ReadBacklog(dir)
		if err != nil {
			return ingesthealth.SpoolBacklog{}, err
		}
		return ingesthealth.SpoolBacklog{Bytes: b.Bytes, LastAdvanceAt: b.LastAdvanceAt}, nil
	}
}
