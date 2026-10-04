package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

// telemetryDBs are the writer's infrastructure telemetry files. Either may be
// nil: telemetry is an add-on, so a file that fails to open disables its
// feature and is reported, but never keeps the writer from serving events.
type telemetryDBs struct {
	Security *telemetrydb.DB
	Metrics  *telemetrydb.DB
}

// openTelemetry opens both files, starts their maintenance loops (checkpoint
// and size cap) on wg, and returns a close func that checkpoints and closes
// them; call it after the loops have stopped.
func openTelemetry(ctx context.Context, cfg config.Telemetry, wg *sync.WaitGroup, log *slog.Logger) (telemetryDBs, func()) {
	var t telemetryDBs
	t.Security = openTelemetryDB(ctx, telemetrydb.Security, cfg.SecurityDBPath, cfg.SecurityMaxBytes, wg, log)
	t.Metrics = openTelemetryDB(ctx, telemetrydb.Metrics, cfg.MetricsDBPath, cfg.MetricsMaxBytes, wg, log)
	return t, func() {
		for _, d := range []*telemetrydb.DB{t.Security, t.Metrics} {
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
