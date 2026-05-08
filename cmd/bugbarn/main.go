package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

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
	"github.com/wiebe-xyz/bugbarn/internal/issues"
	"github.com/wiebe-xyz/bugbarn/internal/logstream"
	"github.com/wiebe-xyz/bugbarn/internal/selflog"
	"github.com/wiebe-xyz/bugbarn/internal/service"
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
		return runReader(cfg, logger)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Wire the domain event bus and alert evaluator.
	bus := &domainevents.Bus{}
	alertRepo := alert.NewSQLiteRepository(store.DB())
	deliverer := alert.NewDeliverer()
	evaluator := alert.NewEvaluator(alertRepo, deliverer, cfg.PublicURL, logger.With("component", "alert-evaluator"))
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
	go runBackgroundWorker(ctx, eventSpool, cfg.SpoolDir, store, eventPub, selfReporting, workerStatus)

	digest.StartScheduler(ctx, cfg.Digest, store)
	analytics.StartWorker(ctx, store, cfg.AnalyticsRetentionDays)

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
	if len(cfg.TrustedProxies) > 0 {
		apiServer.SetTrustedProxies(cfg.TrustedProxies)
	}
	apiServer.SetDBPath(cfg.DBPath)
	apiServer.SetWorkerStatus(workerStatus)
	apiServer.SetAutoApproveProjects(cfg.AutoApproveProjects)
	apiServer.SetFunnelBarnConfig(cfg.FunnelBarnEndpoint, cfg.FunnelBarnAPIKey)
	if selfReporting {
		apiServer.SetSelfReportingConfig(cfg.SelfAPIKey, cfg.SelfProject)
	}
	if cfg.MaxSourceMapBytes > 0 {
		apiServer.SetMaxSourceMapBytes(cfg.MaxSourceMapBytes)
	}
	var httpHandler http.Handler = apiServer
	httpHandler = tracing.Middleware(httpHandler)
	if selfReporting {
		httpHandler = bb.RecoverMiddleware(httpHandler)
	}

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpHandler,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if selfReporting {
			bb.Shutdown(2 * time.Second)
		}
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// runReader starts the server in read-only mode. It opens the writer's SQLite
// database directly (WAL mode allows concurrent readers) and forwards all
// writes to the writer pod via HTTP.
func runReader(cfg config.Config, logger *slog.Logger) error {
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

	apiAuthorizer, err := newAPIAuthorizer(cfg, store)
	if err != nil {
		return err
	}
	userAuth, err := auth.NewUserAuthenticator(cfg.AdminUsername, cfg.AdminPassword, cfg.AdminPasswordBcrypt)
	if err != nil {
		return err
	}
	sessionManager := auth.NewSessionManager(cfg.SessionSecret, cfg.SessionTTL)

	handler := ingest.NewHandler(apiAuthorizer, nil, cfg.MaxBodyBytes)

	logHub := logstream.NewHub()
	apiServer := api.NewServerWithAuth(handler, store, userAuth, sessionManager, cfg.AllowedOrigins, logger)
	apiServer.SetLogHub(logHub)
	apiServer.SetSetupConfig(cfg.SessionSecret, cfg.PublicURL)
	apiServer.SetWriteForwarder(forwarder)
	if len(cfg.TrustedProxies) > 0 {
		apiServer.SetTrustedProxies(cfg.TrustedProxies)
	}
	apiServer.SetAutoApproveProjects(cfg.AutoApproveProjects)
	apiServer.SetFunnelBarnConfig(cfg.FunnelBarnEndpoint, cfg.FunnelBarnAPIKey)
	if cfg.MaxSourceMapBytes > 0 {
		apiServer.SetMaxSourceMapBytes(cfg.MaxSourceMapBytes)
	}

	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: tracing.Middleware(apiServer),
	}

	logger.Info("starting in reader mode", "addr", cfg.Addr, "writer_url", cfg.WriterURL)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newAPIAuthorizer(cfg config.Config, store *storage.Store) (*auth.Authorizer, error) {
	var base *auth.Authorizer
	var err error
	if cfg.APIKeySHA256 != "" {
		base, err = auth.NewHashed(cfg.APIKeySHA256)
		if err != nil {
			return nil, err
		}
	} else {
		base = auth.New(cfg.APIKey)
	}
	base = base.WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	if cfg.SessionSecret != "" {
		base = base.WithSetupKeyVerifier(newSetupKeyVerifier(cfg.SessionSecret, store, cfg.AutoApproveProjects))
	}
	return base, nil
}

func newSetupKeyVerifier(secret string, store *storage.Store, autoApprove bool) auth.SetupKeyVerifier {
	return func(ctx context.Context, rawKey, projectSlug string) (int64, bool) {
		expected := setupKey(secret, projectSlug)
		if expected == "" || subtle.ConstantTimeCompare([]byte(rawKey), []byte(expected)) != 1 {
			return 0, false
		}
		var proj storage.Project
		var err error
		if autoApprove {
			proj, err = store.EnsureProject(ctx, projectSlug)
		} else {
			proj, err = store.EnsureProjectPending(ctx, projectSlug)
		}
		if err != nil {
			return 0, false
		}
		keySHA := sha256Hex(rawKey)
		_ = store.EnsureSetupAPIKey(ctx, projectSlug+"-setup", proj.ID, keySHA)
		return proj.ID, true
	}
}

func setupKey(secret, slug string) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("setup:" + slug))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// runWorkerOnce replays queued records into the persistent store for local maintenance.
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

	processed, err := worker.ProcessRecords(records)
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
		"records": len(records),
		"events":  len(processed),
		"issues":  store.Len(),
	})
}

const (
	workerMaxRetries      = 3
	workerRotateThreshold = 64 << 20 // 64 MiB
)

func runBackgroundWorker(ctx context.Context, eventSpool *spool.Spool, spoolDir string, store *storage.Store, svc *service.EventPublisher, selfReporting bool, ws *worker.Status) {
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
