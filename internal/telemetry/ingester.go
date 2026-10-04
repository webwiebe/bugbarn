// Package telemetry is the writer-side service for infrastructure telemetry:
// it normalizes Vector batches (security logs, host metrics), hands them to
// the detection engine, and stores them in the size-capped telemetry files.
//
// Detections run before the insert on purpose. Raw rows are best-effort and
// may be refused when a file is at its cap; detections must not depend on
// them.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// Kinds accepted by Ingest; the same strings as the write-queue kinds.
const (
	KindSecurity = queue.KindSecurity
	KindMetrics  = queue.KindMetrics
)

// ErrUnknownKind is returned for a kind other than KindSecurity/KindMetrics.
var ErrUnknownKind = errors.New("unknown telemetry kind")

// ErrUnavailable is returned when the kind's telemetry file did not open.
var ErrUnavailable = errors.New("telemetry storage unavailable")

// Observer receives every normalized batch before it is stored. The
// detection engine implements it.
type Observer interface {
	ObserveSecurity(ctx context.Context, recs []secnorm.Record)
	ObserveMetrics(ctx context.Context, samples []hostmetrics.Sample, hosts []hostmetrics.HostInfo)
}

var recordCounter metric.Int64Counter

func init() {
	recordCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.telemetry.records",
		metric.WithDescription("Telemetry records ingested, by kind and outcome (stored, full, error, skipped)."),
		metric.WithUnit("{record}"),
	)
}

// Ingester normalizes and stores telemetry batches. Safe for concurrent use.
type Ingester struct {
	security *telemetrydb.DB
	metrics  *telemetrydb.DB
	norm     *hostmetrics.Normalizer
	observer Observer
	log      *slog.Logger
	now      func() time.Time
}

// New builds an Ingester. Either DB may be nil; its kind then fails with
// ErrUnavailable.
func New(security, metrics *telemetrydb.DB, log *slog.Logger) *Ingester {
	return &Ingester{
		security: security,
		metrics:  metrics,
		norm:     hostmetrics.NewNormalizer(),
		log:      log.With("component", "telemetry"),
		now:      time.Now,
	}
}

// SetObserver wires the detection engine. Call before serving traffic.
func (i *Ingester) SetObserver(o Observer) { i.observer = o }

// Security is the security telemetry file, nil when it did not open.
func (i *Ingester) Security() *telemetrydb.DB { return i.security }

// Metrics is the metrics telemetry file, nil when it did not open.
func (i *Ingester) Metrics() *telemetrydb.DB { return i.metrics }

// Ingest processes one Vector batch. A file at its size cap is not an error:
// the batch has been observed by detections and only its raw rows are
// dropped. A failed insert is returned so the caller can retry.
func (i *Ingester) Ingest(ctx context.Context, kind string, body []byte) error {
	switch kind {
	case KindSecurity:
		return i.ingestSecurity(ctx, body)
	case KindMetrics:
		return i.ingestMetrics(ctx, body)
	}
	return ErrUnknownKind
}

func (i *Ingester) ingestSecurity(ctx context.Context, body []byte) error {
	if i.security == nil {
		return ErrUnavailable
	}
	now := i.now().UTC()
	recs, skipped := secnorm.Parse(body, now)
	count(ctx, KindSecurity, "skipped", skipped)
	if len(recs) == 0 {
		return nil
	}
	if i.observer != nil {
		i.observer.ObserveSecurity(ctx, recs)
	}
	return i.store(ctx, KindSecurity, len(recs), i.security.InsertSecurity(ctx, recs, now))
}

func (i *Ingester) ingestMetrics(ctx context.Context, body []byte) error {
	if i.metrics == nil {
		return ErrUnavailable
	}
	samples, hosts, skipped := i.norm.Parse(body)
	count(ctx, KindMetrics, "skipped", skipped)
	if len(samples) == 0 && len(hosts) == 0 {
		return nil
	}
	if i.observer != nil {
		i.observer.ObserveMetrics(ctx, samples, hosts)
	}
	return i.store(ctx, KindMetrics, len(samples), i.metrics.InsertMetrics(ctx, samples, hosts))
}

func (i *Ingester) store(ctx context.Context, kind string, n int, err error) error {
	switch {
	case err == nil:
		count(ctx, kind, "stored", n)
		return nil
	case errors.Is(err, telemetrydb.ErrFull):
		// By design: the cap protects the disk and detections already ran.
		count(ctx, kind, "full", n)
		i.log.Warn("telemetry file at its size cap; raw rows dropped", "kind", kind, "rows", n)
		return nil
	default:
		count(ctx, kind, "error", n)
		if ctx.Err() == nil {
			i.log.Error("telemetry insert failed", "kind", kind, "rows", n, "error", err)
		}
		return err
	}
}

func count(ctx context.Context, kind, outcome string, n int) {
	if n > 0 {
		recordCounter.Add(ctx, int64(n), metric.WithAttributes(
			attribute.String("kind", kind), attribute.String("outcome", outcome)))
	}
}
