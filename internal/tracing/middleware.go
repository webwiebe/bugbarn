package tracing

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := Tracer().Start(r.Context(), fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", rec.status))
		if rec.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}
