package main

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/wiebe-xyz/bugbarn/internal/oomwatch"
)

// runOOMWatch is `bugbarn oomwatch`: the per-cluster watcher that reports
// OOMKilled containers. It opens no database and needs only OOMWATCH_* config.
func runOOMWatch(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return oomwatch.Run(ctx, logger)
}
