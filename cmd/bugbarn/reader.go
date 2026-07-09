package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	bb "github.com/wiebe-xyz/bugbarn-go"
	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/selflog"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func runReader(cfg config.Config, logHandler slog.Handler) error {
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

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

	shutdownTracing, err := tracing.Init(context.Background(), Version)
	if err != nil {
		logger.Warn("tracing init failed", "error", err)
	} else {
		defer shutdownTracing(context.Background())
	}

	store, err := storage.OpenReadOnly(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open read-only storage: %w", err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	forwarder := api.NewWriteForwarder(cfg.WriterURL)

	// The ingest spool is opt-in for readers: only enabled when BUGBARN_SPOOL_DIR
	// is set explicitly in the environment (config.SpoolDir has a default that
	// points at a non-writable path inside the container).
	//
	// Spec 007: when BUGBARN_REDIS_QUEUE_URL is set, the spool drains to the Redis
	// write queue instead of forwarding to the writer over HTTP. The spool remains
	// the durability anchor (cursor advances only after a successful publish), so
	// the lazy queue client is fine — ingest keeps spooling even if Redis is down.
	var ingestSpool *api.SpoolForwarder
	if os.Getenv("BUGBARN_SPOOL_DIR") != "" {
		if cfg.RedisQueueURL != "" {
			writeQueue, qerr := queue.NewRedisQueueLazy(cfg.RedisQueueURL)
			if qerr != nil {
				return fmt.Errorf("open write queue: %w", qerr)
			}
			defer writeQueue.Close()
			ingestSpool, err = api.NewRedisSpoolForwarder(cfg.SpoolDir, writeQueue, cfg.MaxBodyBytes, logger)
			logger.Info("ingest spool draining to redis write queue", "url", cfg.RedisQueueURL)
		} else {
			ingestSpool, err = api.NewSpoolForwarder(cfg.SpoolDir, cfg.WriterURL, cfg.MaxBodyBytes, logger)
		}
		if err != nil {
			return fmt.Errorf("open ingest spool: %w", err)
		}
		defer ingestSpool.Close()
	}

	apiAuthorizer, err := newAPIAuthorizer(cfg, store)
	if err != nil {
		return err
	}
	userAuth, err := auth.NewUserAuthenticator(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminPasswordBcrypt)
	if err != nil {
		return err
	}
	// Readers only exist in a multi-process (CQRS) deployment and validate
	// sessions minted by the writer. Without a shared BUGBARN_SESSION_SECRET each
	// process signs with a different random key, so every writer-issued session is
	// rejected here. Fail fast instead of shipping a login loop.
	if userAuth.Enabled() && strings.TrimSpace(cfg.SessionSecret) == "" {
		return errors.New("reader mode requires BUGBARN_SESSION_SECRET (must match the writer) when authentication is enabled")
	}
	sessionManager := auth.NewSessionManager(cfg.SessionSecret, cfg.SessionTTL)

	handler := ingest.NewHandler(apiAuthorizer, nil, cfg.MaxBodyBytes)

	logHub := logstream.NewHub()
	apiServer := api.NewServerWithAuth(handler, store, userAuth, sessionManager, cfg.AllowedOrigins, logger)
	apiServer.SetLogHub(logHub)
	apiServer.SetSetupConfig(cfg.SessionSecret, cfg.PublicURL)
	apiServer.SetWriteForwarder(forwarder)
	if ingestSpool != nil {
		apiServer.SetIngestSpool(ingestSpool)
	}
	if len(cfg.TrustedProxies) > 0 {
		apiServer.SetTrustedProxies(cfg.TrustedProxies)
	}
	apiServer.SetAutoApproveProjects(cfg.AutoApproveProjects)
	apiServer.SetFunnelBarnConfig(cfg.FunnelBarnEndpoint, cfg.FunnelBarnAPIKey)
	if oidcClient := buildOIDCClient(cfg, logger); oidcClient != nil {
		apiServer.SetOIDCClient(oidcClient)
	}
	if cfg.MaxSourceMapBytes > 0 {
		apiServer.SetMaxSourceMapBytes(cfg.MaxSourceMapBytes)
	}
	startIngestHealthMonitor(ctx, cfg, store, apiServer, logger, nil)
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

	logger.Info("starting in reader mode", "addr", cfg.Addr, "writer_url", cfg.WriterURL, "spool_dir", cfg.SpoolDir)

	// Background drain loop: continuously pumps spooled ingest payloads to the
	// writer. Stops when drainCtx is canceled at shutdown.
	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()
	drainDone := make(chan struct{})
	if ingestSpool != nil {
		go func() {
			defer close(drainDone)
			ingestSpool.Drain(drainCtx)
		}()
	} else {
		close(drainDone)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		// 1. Stop accepting new HTTP traffic.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)

		// 2. Drain whatever is left in the spool before exiting, so the next
		//    reader version doesn't take over with our backlog still on disk.
		//    Bounded so a permanently-down writer can't block pod termination.
		if ingestSpool != nil {
			drainCancel() // stop the background loop so DrainOnce has the spool to itself
			<-drainDone
			pending := ingestSpool.Pending()
			if pending > 0 {
				logger.Info("draining ingest spool before exit", "pending", pending)
				drainDeadline, cancelDrain := context.WithTimeout(context.Background(), 45*time.Second)
				for {
					err := ingestSpool.DrainOnce(drainDeadline)
					if err == nil {
						logger.Info("ingest spool drained")
						break
					}
					if drainDeadline.Err() != nil {
						logger.Warn("ingest spool drain incomplete", "error", err, "remaining", ingestSpool.Pending())
						break
					}
					// Transient error (e.g. writer restarting) — back off and retry.
					select {
					case <-drainDeadline.Done():
						logger.Warn("ingest spool drain incomplete", "error", err, "remaining", ingestSpool.Pending())
					case <-time.After(2 * time.Second):
						continue
					}
					break
				}
				cancelDrain()
			}
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

// buildOIDCClient returns an OIDC adapter when all four BUGBARN_OIDC_* vars
// are set, or nil otherwise (in which case the local single-user login is the
// only auth path). Discovery is lazy so an unreachable issuer at startup does
// not crash the process.
