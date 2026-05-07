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
func Init(ctx context.Context, version string) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	exporter, err := otlptracehttp.New(initCtx,
		otlptracehttp.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, err
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

	res, err := resource.New(ctx, attrs...)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
