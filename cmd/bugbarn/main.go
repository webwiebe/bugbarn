package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/issues"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run owns process wiring: it opens storage, starts the worker, and serves the API.
func run() error {
	cfg := loadConfig()
	if len(os.Args) > 1 && os.Args[1] == "worker-once" {
		return runWorkerOnce(cfg)
	}

	store, err := storage.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	eventSpool, err := spool.NewWithLimit(cfg.spoolDir, cfg.maxSpoolBytes)
	if err != nil {
		return err
	}
	defer eventSpool.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go runBackgroundWorker(ctx, cfg.spoolDir, store)

	apiAuthorizer, err := newAPIAuthorizer(cfg)
	if err != nil {
		return err
	}
	userAuth, err := auth.NewUserAuthenticator(cfg.adminUsername, cfg.adminPassword, cfg.adminPasswordBcrypt)
	if err != nil {
		return err
	}
	sessionManager := auth.NewSessionManager(cfg.sessionSecret, cfg.sessionTTL)
	handler := ingest.NewHandler(apiAuthorizer, eventSpool, cfg.maxBodyBytes)
	server := &http.Server{
		Addr:    cfg.addr,
		Handler: api.NewServerWithAuth(handler, store, userAuth, sessionManager),
	}

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

type config struct {
	addr                string
	apiKey              string
	apiKeySHA256        string
	adminUsername       string
	adminPassword       string
	adminPasswordBcrypt string
	sessionSecret       string
	sessionTTL          time.Duration
	spoolDir            string
	dbPath              string
	maxBodyBytes        int64
	maxSpoolBytes       int64
}

func loadConfig() config {
	cfg := config{
		addr:                getenv("BUGBARN_ADDR", ":8080"),
		apiKey:              os.Getenv("BUGBARN_API_KEY"),
		apiKeySHA256:        os.Getenv("BUGBARN_API_KEY_SHA256"),
		adminUsername:       os.Getenv("BUGBARN_ADMIN_USERNAME"),
		adminPassword:       os.Getenv("BUGBARN_ADMIN_PASSWORD"),
		adminPasswordBcrypt: os.Getenv("BUGBARN_ADMIN_PASSWORD_BCRYPT"),
		sessionSecret:       os.Getenv("BUGBARN_SESSION_SECRET"),
		sessionTTL:          12 * time.Hour,
		spoolDir:            getenv("BUGBARN_SPOOL_DIR", ".data/spool"),
		dbPath:              getenv("BUGBARN_DB_PATH", ".data/bugbarn.db"),
		maxBodyBytes:        1 << 20,
	}

	if raw := os.Getenv("BUGBARN_MAX_BODY_BYTES"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			cfg.maxBodyBytes = parsed
		}
	}
	if raw := os.Getenv("BUGBARN_MAX_SPOOL_BYTES"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			cfg.maxSpoolBytes = parsed
		}
	}
	if raw := os.Getenv("BUGBARN_SESSION_TTL_SECONDS"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			cfg.sessionTTL = time.Duration(parsed) * time.Second
		}
	}

	return cfg
}

func newAPIAuthorizer(cfg config) (*auth.Authorizer, error) {
	if cfg.apiKeySHA256 != "" {
		return auth.NewHashed(cfg.apiKeySHA256)
	}
	return auth.New(cfg.apiKey), nil
}

// runWorkerOnce replays queued records into the persistent store for local maintenance.
func runWorkerOnce(cfg config) error {
	persistentStore, err := storage.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer persistentStore.Close()

	records, err := spool.ReadRecords(spool.Path(cfg.spoolDir))
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
		if _, _, err := persistentStore.PersistProcessedEvent(context.Background(), item); err != nil {
			return err
		}
	}

	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"records": len(records),
		"events":  len(processed),
		"issues":  store.Len(),
	})
}

func runBackgroundWorker(ctx context.Context, spoolDir string, store *storage.Store) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	processedCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			records, err := spool.ReadRecords(spool.Path(spoolDir))
			if err != nil {
				log.Printf("worker read spool: %v", err)
				continue
			}
			if processedCount > len(records) {
				processedCount = 0
			}
			for _, record := range records[processedCount:] {
				processed, err := worker.ProcessRecord(record)
				if err != nil {
					log.Printf("worker process record %s: %v", record.IngestID, err)
					continue
				}
				if _, _, err := store.PersistProcessedEvent(ctx, processed); err != nil {
					log.Printf("worker persist record %s: %v", record.IngestID, err)
					continue
				}
				processedCount++
			}
		}
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
