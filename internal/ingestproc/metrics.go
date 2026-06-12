package ingestproc

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// consumerMetrics holds the OTel instruments for the writer's queue consumer.
// Instruments come from the global meter; before tracing.Init wires a
// MeterProvider they are valid no-op instruments, so construction never fails in
// tests or when telemetry is disabled.
type consumerMetrics struct {
	items    metric.Int64Counter
	duration metric.Float64Histogram
	reg      metric.Registration
}

// newConsumerMetrics builds the consumer instruments. depth, when non-nil, is
// polled by an observable gauge to report the live write-queue backlog so a
// growing queue is visible in telemetry rather than only in logs.
func newConsumerMetrics(depth func(context.Context) (int64, error)) *consumerMetrics {
	m := tracing.Meter()
	items, _ := m.Int64Counter(
		"bugbarn.consumer.items",
		metric.WithDescription("Queue items processed by the writer consumer, by kind and outcome."),
		metric.WithUnit("{item}"),
	)
	duration, _ := m.Float64Histogram(
		"bugbarn.consumer.item.duration",
		metric.WithDescription("Wall-clock time to persist a single queue item."),
		metric.WithUnit("ms"),
	)
	cm := &consumerMetrics{items: items, duration: duration}

	if depth != nil {
		gauge, err := m.Int64ObservableGauge(
			"bugbarn.write_queue.depth",
			metric.WithDescription("Pending entries on the Redis write queue."),
			metric.WithUnit("{entry}"),
		)
		if err == nil {
			cm.reg, _ = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
				n, err := depth(ctx)
				if err != nil {
					return nil // transient Redis error: skip this observation.
				}
				o.ObserveInt64(gauge, n)
				return nil
			}, gauge)
		}
	}
	return cm
}

// record reports one processed item: a count tagged by kind+outcome and the
// persist duration tagged by kind.
func (m *consumerMetrics) record(ctx context.Context, kind, outcome string, durMs float64) {
	m.items.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("outcome", outcome),
	))
	m.duration.Record(ctx, durMs, metric.WithAttributes(attribute.String("kind", kind)))
}

// close unregisters the observable-gauge callback.
func (m *consumerMetrics) close() {
	if m.reg != nil {
		_ = m.reg.Unregister()
	}
}
