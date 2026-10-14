package main

import (
	"context"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// withSetupKeyUsage wraps a setup key verifier so every accepted setup key is
// visible. The verifier runs only when no stored key matched, so a hit means a
// project still ingests with its derived setup key instead of a stored one.
//
// It increments the bugbarn.auth.setup_key counter per project slug and logs
// each slug once per process at Info. The slug is bounded: it is counted only
// after the HMAC check passed, so a caller cannot invent label values. The key
// itself is never logged.
func withSetupKeyUsage(next auth.SetupKeyVerifier, logger *slog.Logger) auth.SetupKeyVerifier {
	uses, _ := tracing.Meter().Int64Counter(
		"bugbarn.auth.setup_key",
		metric.WithDescription("Ingest requests accepted through a project's derived setup key."),
		metric.WithUnit("{request}"),
	)
	var seen sync.Map
	return func(ctx context.Context, rawKey, projectSlug string) (int64, bool) {
		pid, ok := next(ctx, rawKey, projectSlug)
		if !ok {
			return pid, false
		}
		uses.Add(ctx, 1, metric.WithAttributes(attribute.String("project", projectSlug)))
		if _, dup := seen.LoadOrStore(projectSlug, struct{}{}); !dup {
			logger.Info("auth: request accepted with a setup key", "project", projectSlug)
		}
		return pid, true
	}
}
