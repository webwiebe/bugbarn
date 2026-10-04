package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

// openTelemetry opens the writer's infrastructure telemetry files, starts
// their maintenance loops (checkpoint and size cap) on wg, and returns the
// ingester over them plus a close func that checkpoints and closes the files;
// call it after the loops have stopped.
//
// Telemetry is an add-on: a file that fails to open disables its kind and is
// reported, but never keeps the writer from serving events.
func openTelemetry(ctx context.Context, cfg config.Telemetry, wg *sync.WaitGroup, log *slog.Logger) (*telemetry.Ingester, func()) {
	sec := openTelemetryDB(ctx, telemetrydb.Security, cfg.SecurityDBPath, cfg.SecurityMaxBytes, wg, log)
	met := openTelemetryDB(ctx, telemetrydb.Metrics, cfg.MetricsDBPath, cfg.MetricsMaxBytes, wg, log)
	return telemetry.New(sec, met, log), func() {
		for _, d := range []*telemetrydb.DB{sec, met} {
			if d != nil {
				d.FinalCheckpoint(log)
				_ = d.Close()
			}
		}
	}
}

func openTelemetryDB(ctx context.Context, spec telemetrydb.Spec, path string, maxBytes int64, wg *sync.WaitGroup, log *slog.Logger) *telemetrydb.DB {
	d, err := telemetrydb.Open(ctx, spec, path, maxBytes)
	if err != nil {
		log.Error("telemetry database unavailable; feature disabled", "db", spec.Name, "path", path, "error", err)
		return nil
	}
	log.Info("telemetry database open", "db", spec.Name, "path", d.Path(), "max_bytes", maxBytes)
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.RunMaintenance(ctx, telemetrydb.DefaultMaintenanceInterval, log)
	}()
	return d
}
