package tracing

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// httpMetrics holds the OTel instruments for the HTTP server middleware.
// Instruments come from the global meter; before tracing.Init wires a
// MeterProvider they are valid no-op instruments, so construction never fails
// in tests or when telemetry is disabled.
type httpMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

var (
	httpMetricsOnce sync.Once
	httpMetricsInst *httpMetrics
)

// getHTTPMetrics builds the middleware instruments the first time it's
// called and reuses them thereafter, regardless of how many times
// Middleware() wraps a handler.
func getHTTPMetrics() *httpMetrics {
	httpMetricsOnce.Do(func() {
		m := Meter()
		requests, _ := m.Int64Counter(
			"bugbarn.http.requests",
			metric.WithDescription("HTTP requests handled by the server, by method, route, and status class."),
			metric.WithUnit("{request}"),
		)
		duration, _ := m.Float64Histogram(
			"bugbarn.http.request.duration",
			metric.WithDescription("Wall-clock time to handle an HTTP request, by method and route."),
			metric.WithUnit("ms"),
		)
		httpMetricsInst = &httpMetrics{requests: requests, duration: duration}
	})
	return httpMetricsInst
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func Middleware(next http.Handler) http.Handler {
	hm := getHTTPMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract W3C traceparent from incoming request headers.
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		ctx, span := Tracer().Start(ctx, fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))
		durMs := float64(time.Since(start)) / float64(time.Millisecond)

		span.SetAttributes(attribute.Int("http.status_code", rec.status))
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}

		statusClass := fmt.Sprintf("%dxx", rec.status/100)
		hm.requests.Add(ctx, 1, metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("route", r.URL.Path),
			attribute.String("status_class", statusClass),
		))
		hm.duration.Record(ctx, durMs, metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("route", r.URL.Path),
		))
	})
}
