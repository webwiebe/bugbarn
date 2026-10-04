package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/cfpoller"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/detect"
	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/service/detectionrules"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
)

// openTelemetry opens the writer's infrastructure telemetry files, starts
// their maintenance loops (checkpoint and size cap, plus the hourly rollup and
// retention for metrics.db) on wg, and returns the
// ingester over them plus a close func that checkpoints and closes the files;
// call it after the loops have stopped.
//
// Telemetry is an add-on: a file that fails to open disables its kind and is
// reported, but never keeps the writer from serving events.
func openTelemetry(ctx context.Context, cfg config.Telemetry, wg *sync.WaitGroup, log *slog.Logger) (*telemetry.Ingester, func()) {
	sec := openTelemetryDB(ctx, telemetrydb.Security, cfg.SecurityDBPath, cfg.SecurityMaxBytes, wg, log)
	met := openTelemetryDB(ctx, telemetrydb.Metrics, cfg.MetricsDBPath, cfg.MetricsMaxBytes, wg, log)
	if met != nil {
		ret := telemetrydb.Retention{
			Raw:    time.Duration(cfg.MetricsRawRetentionDays) * 24 * time.Hour,
			Hourly: time.Duration(cfg.MetricsHourlyRetentionDays) * 24 * time.Hour,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			met.RunMetricsJobs(ctx, telemetrydb.DefaultMaintenanceInterval, ret, log)
		}()
	}
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

// startDetections runs the detection engine over the ingester's telemetry:
// the given rules, the heartbeat ticker and the emitter that persists each
// detection as an event in the telemetry project through proc.
func startDetections(ctx context.Context, ing *telemetry.Ingester, proc *ingestproc.Processor, rules []detect.Rule, project string, wg *sync.WaitGroup, log *slog.Logger) *detect.Engine {
	engine := detect.NewEngine(rules, detectionBuffer, log)
	if met := ing.Metrics(); met != nil {
		hosts, err := met.Hosts(ctx)
		if err != nil {
			log.Warn("detect: could not seed hosts for the heartbeat", "error", err)
		}
		seed := make([]hostmetrics.HostInfo, len(hosts))
		for i, h := range hosts {
			seed[i] = hostmetrics.HostInfo{Host: h.Host, Cores: h.Cores, LastSeen: h.LastSeen}
		}
		engine.SeedHosts(seed)
	}
	ing.SetObserver(engine)

	emitter := detect.NewEmitter(engine.Detections(), func(ctx context.Context, rec spool.Record) (bool, error) {
		res := proc.PersistRecord(ctx, rec)
		switch res.Outcome {
		case ingestproc.OutcomeSuccess, ingestproc.OutcomeHeld:
			return false, nil
		case ingestproc.OutcomeTransient:
			return true, res.Err
		}
		return false, res.Err
	}, project, log)
	wg.Add(2)
	go func() {
		defer wg.Done()
		emitter.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		engine.RunHeartbeat(ctx, time.Minute)
	}()
	return engine
}

// detectionBuffer bounds detections waiting for the emitter. Cooldowns keep
// the rate low; a full buffer means persisting is stuck.
const detectionBuffer = 1024

// telemetryViewer is the part of *api.Server the read endpoints need.
type telemetryViewer interface {
	SetTelemetryViews(security, metrics telemetrydb.Source)
}

// wireWriterTelemetryViews serves the read endpoints from the handles the
// writer (or monolith) already writes through.
func wireWriterTelemetryViews(srv telemetryViewer, ing *telemetry.Ingester) {
	srv.SetTelemetryViews(telemetrydb.Fixed(ing.Security()), telemetrydb.Fixed(ing.Metrics()))
}

// wireReaderTelemetryViews serves the read endpoints on a reader pod. The
// writer creates the files on the shared volume, so they are opened
// read-only on first use; until then the endpoints answer 503. The returned
// func closes whatever was opened.
func wireReaderTelemetryViews(srv telemetryViewer, cfg config.Telemetry) func() {
	sec := telemetrydb.NewLazy(telemetrydb.Security, cfg.SecurityDBPath)
	met := telemetrydb.NewLazy(telemetrydb.Metrics, cfg.MetricsDBPath)
	srv.SetTelemetryViews(sec, met)
	return func() {
		_ = sec.Close()
		_ = met.Close()
	}
}

// detectionRules returns the rule set to start the engine with: built-ins
// plus stored overrides. If the stored rules cannot be read the engine still
// starts on the built-ins; the service has logged the failure.
func detectionRules(ctx context.Context, svc *detectionrules.Service) []detect.Rule {
	rules, err := svc.Effective(ctx)
	if err != nil {
		return detect.Defaults()
	}
	return rules
}

// startCloudflarePoller pulls Cloudflare firewall events into the security
// telemetry file when a token and zones are configured.
func startCloudflarePoller(ctx context.Context, cfg config.Cloudflare, ing *telemetry.Ingester, wg *sync.WaitGroup, log *slog.Logger) {
	if !cfg.Enabled() {
		return
	}
	if ing.Security() == nil {
		log.Warn("cloudflare poller not started: security telemetry file unavailable")
		return
	}
	p := cfpoller.New(cfpoller.Config{Token: cfg.APIToken, Zones: cfg.ZoneIDs, Interval: cfg.PollInterval}, ing, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Run(ctx)
	}()
}
