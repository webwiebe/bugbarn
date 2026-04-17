package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/wiebe-xyz/bugbarn/internal/api"
	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/issues"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run owns process wiring: it opens storage, starts the worker, and serves the API.
func run() error {
	cfg := loadConfig()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "worker-once":
			return runWorkerOnce(cfg)
		case "user":
			return runUserCmd(cfg, os.Args[2:])
		case "project":
			return runProjectCmd(cfg, os.Args[2:])
		case "apikey":
			return runAPIKeyCmd(cfg, os.Args[2:])
		}
	}

	if cfg.sessionSecret == "" {
		log.Println("warning: BUGBARN_SESSION_SECRET is not set; sessions will not persist across restarts")
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

	apiAuthorizer, err := newAPIAuthorizer(cfg, store)
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
		Handler: api.NewServerWithAuth(handler, store, userAuth, sessionManager, cfg.allowedOrigins),
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
	allowedOrigins      []string
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

	if raw := os.Getenv("BUGBARN_ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				cfg.allowedOrigins = append(cfg.allowedOrigins, trimmed)
			}
		}
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

func newAPIAuthorizer(cfg config, store *storage.Store) (*auth.Authorizer, error) {
	var base *auth.Authorizer
	var err error
	if cfg.apiKeySHA256 != "" {
		base, err = auth.NewHashed(cfg.apiKeySHA256)
		if err != nil {
			return nil, err
		}
	} else {
		base = auth.New(cfg.apiKey)
	}
	// Always wire in DB-based key lookup so keys created via the CLI work too.
	return base.WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey), nil
}

// runUserCmd handles: bugbarn user create --username=X --password=Y
func runUserCmd(cfg config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn user <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("user create", flag.ContinueOnError)
		username := fs.String("username", os.Getenv("BUGBARN_ADMIN_USERNAME"), "username")
		password := fs.String("password", os.Getenv("BUGBARN_ADMIN_PASSWORD"), "plaintext password")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*username = strings.TrimSpace(*username)
		*password = strings.TrimSpace(*password)
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		if *password == "" {
			return fmt.Errorf("--password is required")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		store, err := storage.Open(cfg.dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.UpsertUser(context.Background(), *username, string(hash)); err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		fmt.Printf("user %q created/updated\n", *username)
		return nil
	default:
		return fmt.Errorf("unknown user subcommand %q", args[0])
	}
}

// runProjectCmd handles: bugbarn project create --name=X [--slug=Y]
func runProjectCmd(cfg config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn project <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("project create", flag.ContinueOnError)
		name := fs.String("name", "", "project display name")
		slug := fs.String("slug", "", "project slug (defaults to slugified name)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		if *slug == "" {
			*slug = toSlug(*name)
		}
		if !slugPattern.MatchString(*slug) {
			return fmt.Errorf("invalid slug %q: must be lowercase alphanumeric with hyphens", *slug)
		}
		store, err := storage.Open(cfg.dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		p, err := store.CreateProject(context.Background(), *name, *slug)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"id":   p.ID,
			"name": p.Name,
			"slug": p.Slug,
		})
	default:
		return fmt.Errorf("unknown project subcommand %q", args[0])
	}
}

// runAPIKeyCmd handles: bugbarn apikey create --project=default --name=my-app
func runAPIKeyCmd(cfg config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn apikey <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("apikey create", flag.ContinueOnError)
		projectSlug := fs.String("project", "default", "project slug")
		name := fs.String("name", "", "key name/label")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		store, err := storage.Open(cfg.dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		ctx := context.Background()
		project, err := store.ProjectBySlug(ctx, *projectSlug)
		if err != nil {
			// Auto-create the project if it doesn't exist yet.
			project, err = store.CreateProject(ctx, *projectSlug, *projectSlug)
			if err != nil {
				return fmt.Errorf("create project %q: %w", *projectSlug, err)
			}
			fmt.Printf("Project %q created automatically.\n", *projectSlug)
		}
		// Generate a 32-byte random key.
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("generate key: %w", err)
		}
		plaintext := hex.EncodeToString(raw[:])
		sum := sha256.Sum256([]byte(plaintext))
		keySHA256 := hex.EncodeToString(sum[:])

		key, err := store.CreateAPIKey(ctx, *name, project.ID, keySHA256)
		if err != nil {
			return fmt.Errorf("create api key: %w", err)
		}
		fmt.Printf("API key created (id=%d, project=%s, name=%s)\n", key.ID, project.Slug, key.Name)
		fmt.Printf("Key (shown once, store securely): %s\n", plaintext)
		return nil
	default:
		return fmt.Errorf("unknown apikey subcommand %q", args[0])
	}
}

// toSlug converts a display name to a URL-safe slug.
func toSlug(name string) string {
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
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

const (
	workerMaxRetries      = 3
	workerRotateThreshold = 64 << 20 // 64 MiB
)

func runBackgroundWorker(ctx context.Context, spoolDir string, store *storage.Store) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Restore cursor position from disk so we never re-process already-handled records.
	offset, err := spool.ReadCursor(spoolDir)
	if err != nil {
		log.Printf("worker: failed to read cursor (starting from 0): %v", err)
		offset = 0
	}

	// retryCounts tracks per-ingest-ID failure counts within this process lifetime.
	retryCounts := make(map[string]int)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := spool.ReadRecordsFrom(spool.Path(spoolDir), offset)
			if err != nil {
				log.Printf("worker read spool: %v", err)
				continue
			}

			for _, entry := range entries {
				record := entry.Record

				processed, err := worker.ProcessRecord(record)
				if err != nil {
					retryCounts[record.IngestID]++
					log.Printf("worker process record %s (attempt %d): %v", record.IngestID, retryCounts[record.IngestID], err)
					if retryCounts[record.IngestID] >= workerMaxRetries {
						log.Printf("worker dead-lettering record %s after %d attempts", record.IngestID, retryCounts[record.IngestID])
						if dlErr := spool.AppendDeadLetter(spoolDir, record); dlErr != nil {
							log.Printf("worker dead-letter write %s: %v", record.IngestID, dlErr)
						}
						delete(retryCounts, record.IngestID)
						// Advance cursor past this dead-lettered record.
						offset = entry.EndOffset
						if err := spool.WriteCursor(spoolDir, offset); err != nil {
							log.Printf("worker write cursor: %v", err)
						}
					}
					// Stop processing this batch; retry remaining records next tick.
					break
				}
				// Resolve project from the slug stored in the spool record.
				persistCtx := ctx
				if record.ProjectSlug != "" {
					if proj, err := store.EnsureProject(ctx, record.ProjectSlug); err == nil {
						persistCtx = storage.WithProjectID(ctx, proj.ID)
					} else {
						log.Printf("worker ensure project %q: %v", record.ProjectSlug, err)
					}
				}
				// Annotate JS stack frames with original positions from stored source maps.
				processed.Event = worker.SymbolicateEvent(persistCtx, processed.Event, store)
				if _, _, err := store.PersistProcessedEvent(persistCtx, processed); err != nil {
					retryCounts[record.IngestID]++
					log.Printf("worker persist record %s (attempt %d): %v", record.IngestID, retryCounts[record.IngestID], err)
					if retryCounts[record.IngestID] >= workerMaxRetries {
						log.Printf("worker dead-lettering record %s after %d persist attempts", record.IngestID, retryCounts[record.IngestID])
						if dlErr := spool.AppendDeadLetter(spoolDir, record); dlErr != nil {
							log.Printf("worker dead-letter write %s: %v", record.IngestID, dlErr)
						}
						delete(retryCounts, record.IngestID)
						// Advance cursor past this dead-lettered record.
						offset = entry.EndOffset
						if err := spool.WriteCursor(spoolDir, offset); err != nil {
							log.Printf("worker write cursor: %v", err)
						}
					}
					// Stop processing this batch; retry remaining records next tick.
					break
				}

				delete(retryCounts, record.IngestID)
				// Advance cursor after each successfully processed record.
				offset = entry.EndOffset
				if err := spool.WriteCursor(spoolDir, offset); err != nil {
					log.Printf("worker write cursor: %v", err)
				}
			}

			// Rotate the active spool file once it exceeds the threshold, so old
			// segments can eventually be archived or deleted.
			if err := spool.RotateIfExceedsPath(spoolDir, workerRotateThreshold); err != nil {
				log.Printf("worker rotate spool: %v", err)
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
