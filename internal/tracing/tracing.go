package tracing

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "bugbarn"

func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Meter returns the global bugbarn meter. No-op (records nowhere) until Init has
// configured a MeterProvider, so instruments can be created unconditionally.
func Meter() metric.Meter {
	return otel.Meter(tracerName)
}

// metricsHandler serves the Prometheus exposition of all instruments. nil until
// Init wires the Prometheus exporter; callers must nil-check before mounting.
var metricsHandler http.Handler

// MetricsHandler returns the Prometheus /metrics handler, or nil if metrics
// failed to initialize. Mount it outside any tracing middleware so scrapes are
// not themselves traced.
func MetricsHandler() http.Handler { return metricsHandler }

// Init wires telemetry for the process:
//
//   - Metrics: a Prometheus pull exporter, always enabled. Instruments are
//     exposed at the handler returned by MetricsHandler() for scraping or
//     on-demand curl. This needs no backend, which suits an environment with no
//     OTLP metrics collector.
//   - Traces: an OTLP HTTP exporter, enabled only when OTEL_EXPORTER_OTLP_ENDPOINT
//     is set. Config is read from the standard OTEL env vars:
//     OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS, OTEL_SERVICE_NAME.
//
// Returns a shutdown function that flushes pending telemetry. Every failure is
// non-fatal: telemetry degrades to no-ops rather than blocking startup.
func Init(_ context.Context, version string) (shutdown func(context.Context) error, err error) {
	res := buildResource(version)

	// Metrics: Prometheus pull exporter. Registers with the default Prometheus
	// registry; MetricsHandler() then serves that registry.
	var mp *sdkmetric.MeterProvider
	if promExp, perr := prometheus.New(); perr == nil {
		mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(promExp),
		)
		otel.SetMeterProvider(mp)
		metricsHandler = promhttp.Handler()
	}

	// Traces: OTLP HTTP, only when an endpoint is configured.
	var tp *sdktrace.TracerProvider
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		tp = initTraces(res)
	}

	return func(ctx context.Context) error {
		var shutErr error
		if tp != nil {
			shutErr = tp.Shutdown(ctx)
		}
		if mp != nil {
			if e := mp.Shutdown(ctx); shutErr == nil {
				shutErr = e
			}
		}
		return shutErr
	}, nil
}

// initTraces builds and registers the OTLP trace pipeline. Returns nil on any
// setup error so the caller runs without tracing rather than blocking.
func initTraces(res *resource.Resource) *sdktrace.TracerProvider {
	// Short timeout so a slow/unreachable SpanBarn never blocks startup; the OTLP
	// client retries exports in the background regardless.
	initCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exporter, err := otlptracehttp.New(initCtx,
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
		otlptracehttp.WithTimeout(2*time.Second),
	)
	if err != nil {
		// Warn (not Error): this runs during Init, before some callers wrap the
		// default logger with the selflog/BugBarn self-report handler, and an
		// Error here would otherwise trigger a self-capture in the middle of
		// telemetry bootstrap. Traces are best-effort by design (see doc comment
		// above), so Warn is also the right severity on its own merits.
		slog.Warn("tracing: failed to build OTLP trace exporter; traces disabled", "error", err)
		return nil
	}

	batcher := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithExportTimeout(5*time.Second),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(NewTailSampler(batcher, 0.1)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp
}

// buildResource assembles the OTel resource (service name + version) shared by
// the trace and metric providers.
func buildResource(version string) *resource.Resource {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "bugbarn"
	}
	attrs := []resource.Option{
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	}
	if version != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(version)))
	}
	res, err := resource.New(context.Background(), attrs...)
	if err != nil {
		return resource.Default()
	}
	return res
}
