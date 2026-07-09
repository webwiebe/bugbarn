package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	bb "github.com/wiebe-xyz/bugbarn-go"
	"github.com/wiebe-xyz/bugbarn/internal/alert"
	"github.com/wiebe-xyz/bugbarn/internal/analytics"
	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/cli"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/selflog"
	"github.com/wiebe-xyz/bugbarn/internal/service"
	logsvc "github.com/wiebe-xyz/bugbarn/internal/service/logs"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// Version and BuildTime are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run owns process wiring: it opens storage, starts the worker, and serves the API.
func run() error {
	cfg := config.Load()

	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	shutdownTracing, err := tracing.Init(context.Background(), Version)
	if err != nil {
		logger.Warn("tracing init failed", "error", err)
	} else {
		defer shutdownTracing(context.Background())
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("bugbarn %s (built %s)\n", Version, BuildTime)
			return nil
		case "worker-once":
			return runWorkerOnce(cfg)
		case "user":
			return cli.RunUser(cfg, os.Args[2:])
		case "project":
			return cli.RunProject(cfg, os.Args[2:])
		case "apikey":
			return cli.RunAPIKey(cfg, os.Args[2:])
		}
	}

	if cfg.Mode == "reader" {
		return runReader(cfg, logHandler)
	}

	if cfg.SessionSecret == "" {
		logger.Warn("BUGBARN_SESSION_SECRET is not set; sessions will not persist across restarts")
	}

	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	eventSpool, err := spool.NewWithLimit(cfg.SpoolDir, cfg.MaxSpoolBytes)
	if err != nil {
		return err
	}
	defer eventSpool.Close()

	mutQueue, err := mutqueue.New(cfg.SpoolDir)
	if err != nil {
		return err
	}
	defer mutQueue.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Wire the domain event bus and alert evaluator.
	bus := &domainevents.Bus{}
	alertRepo := alert.NewSQLiteRepository(store.DB())
	deliverer := alert.NewDeliverer(cfg.Digest.Mail)
	deliverer.SetEventVolumeSource(issueVolumeSource{store: store})
	evaluator := alert.NewEvaluator(alertRepo, deliverer, cfg.PublicURL, cfg.AdminAlertEmail, logger.With("component", "alert-evaluator"))
	bus.Subscribe(evaluator.HandleEvent)

	eventPub := service.NewEventPublisher(bus)

	selfReporting := cfg.SelfEndpoint != "" && cfg.SelfAPIKey != ""
	if selfReporting {
		bb.Init(bb.Options{
			APIKey:      cfg.SelfAPIKey,
			Endpoint:    cfg.SelfEndpoint,
			ProjectSlug: cfg.SelfProject,
		})
		logger = slog.New(selflog.NewHandler(logHandler))
		slog.SetDefault(logger)
		logger.Info("self-reporting enabled", "endpoint", cfg.SelfEndpoint)
	}

	workerStatus := worker.NewStatus()
	var bgWg sync.WaitGroup
	bgWg.Add(1)
	go func() {
		defer bgWg.Done()
		runBackgroundWorker(ctx, eventSpool, cfg.SpoolDir, store, eventPub, selfReporting, workerStatus, mutQueue)
	}()

	digest.StartScheduler(ctx, cfg.Digest, store, &bgWg)
	analytics.StartWorker(ctx, store, cfg.AnalyticsRetentionDays, &bgWg)

	// Spec 007: when a Redis write queue is configured, a single consumer drains
	// it into the DB. This decouples ingest producers (reader pods) from the
	// writer so a slow writer can't trigger the HTTP-forward retry storm that
	// wedged production. Connect in a goroutine so startup never blocks on Redis;
	// the legacy file-spool worker above stays as the fallback path until
	// producers are switched to Redis (phase 3) and it is retired (phase 5).
	// The shared write mutex (nil for now) is wired when retention and the WAL
	// checkpoint move under it in a later phase.
	// The persist pipeline and held-events replayer are shared between the Redis
	// consumer (live ingest) and the project-approval path (backlog drain), so
	// build them once. The replayer works without Redis — approving a pending
	// project drains its backlog even if no consumer is running.
	eventProc := ingestproc.NewProcessor(store, eventPub, logger, cfg.AutoApproveProjects)
	logService := logsvc.New(store.LogStore, logger)
	heldReplayer := ingestproc.NewReplayer(store, eventProc, logService, logger)

	if cfg.RedisQueueURL != "" {
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			writeQueue, qerr := queue.NewRedisQueueWithRetry(ctx, cfg.RedisQueueURL)
			if qerr != nil {
				logger.Warn("write-queue consumer not started", "error", qerr)
				return
			}
			defer writeQueue.Close()
			consumer := ingestproc.NewConsumer(writeQueue, eventProc, logService, nil, logger)
			logger.Info("redis write-queue consumer started", "url", cfg.RedisQueueURL)
			consumer.Run(ctx)
		}()
	}

	apiAuthorizer, err := newAPIAuthorizer(cfg, store)
	if err != nil {
		return err
	}
	userAuth, err := auth.NewUserAuthenticator(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminPasswordBcrypt)
	if err != nil {
		return err
	}
	sessionManager := auth.NewSessionManager(cfg.SessionSecret, cfg.SessionTTL)
	handler := ingest.NewHandler(apiAuthorizer, eventSpool, cfg.MaxBodyBytes)
	go handler.Start(ctx)

	logHub := logstream.NewHub()
	apiServer := api.NewServerWithAuth(handler, store, userAuth, sessionManager, cfg.AllowedOrigins, logger)
	apiServer.SetLogHub(logHub)
	apiServer.SetSetupConfig(cfg.SessionSecret, cfg.PublicURL)
	apiServer.SetDigest(cfg.Digest, store)
	apiServer.SetMutQueue(mutQueue)
	if len(cfg.TrustedProxies) > 0 {
		apiServer.SetTrustedProxies(cfg.TrustedProxies)
	}
	apiServer.SetDBPath(cfg.DBPath)
	apiServer.SetWorkerStatus(workerStatus)
	apiServer.SetAutoApproveProjects(cfg.AutoApproveProjects)
	apiServer.SetHeldReplayer(heldReplayer)
	apiServer.SetFunnelBarnConfig(cfg.FunnelBarnEndpoint, cfg.FunnelBarnAPIKey)
	if selfReporting {
		apiServer.SetSelfReportingConfig(cfg.SelfAPIKey, cfg.SelfProject)
	}
	if oidcClient := buildOIDCClient(cfg, logger); oidcClient != nil {
		apiServer.SetOIDCClient(oidcClient)
	}
	if cfg.MaxSourceMapBytes > 0 {
		apiServer.SetMaxSourceMapBytes(cfg.MaxSourceMapBytes)
	}
	startIngestHealthMonitor(ctx, cfg, store, apiServer, logger, &bgWg)
	apiServer.Start(ctx)

	var httpHandler http.Handler = apiServer
	httpHandler = tracing.Middleware(httpHandler)
	if selfReporting {
		httpHandler = bb.RecoverMiddleware(httpHandler)
	}

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: withMetrics(httpHandler),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		// Wait for the worker to finish its current batch (and write its cursor)
		// before exiting, so a deploy doesn't strand in-flight records.
		drained := make(chan struct{})
		go func() { bgWg.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-shutdownCtx.Done():
			slog.Warn("worker did not drain before shutdown deadline")
		}
		if selfReporting {
			bb.Shutdown(2 * time.Second)
		}
		return shutdownErr
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// withMetrics routes GET /metrics to the Prometheus exposition handler (when
// telemetry is enabled) and everything else to next. /metrics is mounted here,
// outside the tracing middleware, so scrapes are not themselves traced.
func withMetrics(next http.Handler) http.Handler {
	mh := tracing.MetricsHandler()
	if mh == nil {
		return next
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", mh)
	mux.Handle("/", next)
	return mux
}

// runReader starts the server in read-only mode. It opens the writer's SQLite
// database directly (WAL mode allows concurrent readers) and forwards all
// writes to the writer pod via HTTP.
// startIngestHealthMonitor wires the ingest-liveness monitor and publishes its
// snapshot into the health endpoint. It runs in both reader and writer pods: the
// writer additionally sees the WAL, but the reader is what the external health
// probe hits, so it must independently detect a stall (no event persisted for
// too long, or a growing write-queue backlog) even when the writer is wedged —
// the gap that hid the 2026-06-21 outage for five days.

// issueVolumeSource adapts the storage layer to alert.EventVolumeSource. It
// resolves a Jira-style issue ID to its row ID and returns the 24h hourly
// event-count array used to render the regression email's sparkline.
type issueVolumeSource struct {
	store *storage.Store
}

func (s issueVolumeSource) HourlyEventCounts(ctx context.Context, issueID string) ([24]int, error) {
	rowID, err := s.store.IssueStore.IssueRowIDByDisplayID(ctx, issueID)
	if err != nil {
		return [24]int{}, err
	}
	counts, err := s.store.IssueStore.HourlyEventCounts(ctx, []int64{rowID})
	if err != nil {
		return [24]int{}, err
	}
	return counts[rowID], nil
}
