package main

import (
	"context"
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
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()

	eventSpool, err := spool.New(cfg.spoolDir)
	if err != nil {
		return err
	}
	defer eventSpool.Close()

	handler := ingest.NewHandler(auth.New(cfg.apiKey), eventSpool, cfg.maxBodyBytes)
	server := &http.Server{
		Addr:    cfg.addr,
		Handler: api.NewServer(handler),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	addr         string
	apiKey       string
	spoolDir     string
	maxBodyBytes int64
}

func loadConfig() config {
	cfg := config{
		addr:         getenv("BUGBARN_ADDR", ":8080"),
		apiKey:       os.Getenv("BUGBARN_API_KEY"),
		spoolDir:     getenv("BUGBARN_SPOOL_DIR", ".data/spool"),
		maxBodyBytes: 1 << 20,
	}

	if raw := os.Getenv("BUGBARN_MAX_BODY_BYTES"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			cfg.maxBodyBytes = parsed
		}
	}

	return cfg
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
