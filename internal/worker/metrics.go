package worker

import (
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// spoolMetrics holds the OTel instruments for the background spool worker.
// Built once per worker (see newSpoolMetrics) and reused across ticks rather
// than recreated per call, matching the convention in internal/ingestproc.
type spoolMetrics struct {
	eventsProcessed metric.Int64Counter
	deadLetters     metric.Int64Counter
}

// newSpoolMetrics builds the worker instruments from the global meter; before
// tracing.Init wires a MeterProvider they are valid no-op instruments, so
// construction never fails in tests or when telemetry is disabled.
func newSpoolMetrics() *spoolMetrics {
	m := tracing.Meter()
	eventsProcessed, _ := m.Int64Counter(
		"bugbarn.worker.events_processed",
		metric.WithDescription("Spool records processed by the background worker, by outcome."),
		metric.WithUnit("{record}"),
	)
	deadLetters, _ := m.Int64Counter(
		"bugbarn.worker.dead_letters",
		metric.WithDescription("Spool records dead-lettered by the background worker, by stage."),
		metric.WithUnit("{record}"),
	)
	return &spoolMetrics{eventsProcessed: eventsProcessed, deadLetters: deadLetters}
}
