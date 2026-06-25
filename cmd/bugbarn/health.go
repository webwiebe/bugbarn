package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingesthealth"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
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
	monitor := ingesthealth.New(ingesthealth.Config{}, deps, logger)
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
