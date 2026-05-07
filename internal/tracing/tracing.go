package tracing

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "bugbarn"

func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Init sets up the global TracerProvider with an OTLP HTTP exporter.
// Configuration is read from standard OTEL env vars:
//   - OTEL_EXPORTER_OTLP_ENDPOINT (e.g. https://spanbarn.wiebe.xyz)
//   - OTEL_EXPORTER_OTLP_HEADERS  (e.g. Authorization=Bearer <key>)
//   - OTEL_SERVICE_NAME            (defaults to "bugbarn")
//
// Returns a shutdown function that flushes pending spans.
func Init(_ context.Context, version string) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	// Use a short timeout so a slow/unreachable SpanBarn never blocks startup.
	// The OTLP HTTP client retries exports in the background regardless.
	initCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exporter, err := otlptracehttp.New(initCtx,
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
		otlptracehttp.WithTimeout(2*time.Second),
	)
	if err != nil {
		// Non-fatal: run without tracing rather than block the server.
		return func(context.Context) error { return nil }, nil
	}

	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "bugbarn"
	}

	attrs := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	}
	if version != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(version)))
	}

	res, err := resource.New(context.Background(), attrs...)
	if err != nil {
		return func(context.Context) error { return nil }, nil
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithExportTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
