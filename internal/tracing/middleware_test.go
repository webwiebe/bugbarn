package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// withRecordingTracer installs an in-memory tracer provider and the W3C
// propagator for one test and restores the globals afterwards.
func withRecordingTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return exporter
}

func spanAttrs(s tracetest.SpanStub) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes {
		out[kv.Key] = kv.Value
	}
	return out
}

func TestMiddlewareRecordsServerSpan(t *testing.T) {
	exporter := withRecordingTracer(t)
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !trace.SpanFromContext(r.Context()).SpanContext().IsValid() {
			t.Error("handler context carries no span")
		}
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues", nil)
	req.Header.Set("User-Agent", "probe/1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	attrs := spanAttrs(span)
	if span.Name != "GET /api/v1/issues" || span.SpanKind != trace.SpanKindServer {
		t.Errorf("span = %q kind %v", span.Name, span.SpanKind)
	}
	if attrs["http.status_code"].AsInt64() != http.StatusTeapot || attrs["http.user_agent"].AsString() != "probe/1" ||
		attrs["http.method"].AsString() != "GET" || attrs["http.target"].AsString() != "/api/v1/issues" {
		t.Errorf("attributes = %v", attrs)
	}
	if span.Status.Code == codes.Error {
		t.Errorf("a 418 must not mark the span as failed")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("response code = %d", rec.Code)
	}
}

func TestMiddlewareMarksServerErrorsAndDefaultsTo200(t *testing.T) {
	exporter := withRecordingTracer(t)

	Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/boom", nil))
	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fine", nil))

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if spans[0].Status.Code != codes.Error || spans[0].Status.Description != "Internal Server Error" {
		t.Errorf("500 span status = %+v", spans[0].Status)
	}
	if got := spanAttrs(spans[1])["http.status_code"].AsInt64(); got != http.StatusOK {
		t.Errorf("implicit status = %d, want 200", got)
	}
	if spans[1].Status.Code == codes.Error {
		t.Error("a 200 span must not carry an error status")
	}
}

func TestMiddlewareContinuesIncomingTraceparent(t *testing.T) {
	exporter := withRecordingTracer(t)
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("traceparent", parent)
	Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("trace id = %s, want the caller's", got)
	}
	if got := spans[0].Parent.SpanID().String(); got != "b7ad6b7169203331" {
		t.Errorf("parent span id = %s", got)
	}
}

func TestStatusRecorderFlushesUnderlyingWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("wrapped writer does not implement http.Flusher; streaming handlers would break")
			return
		}
		_, _ = w.Write([]byte("chunk"))
		f.Flush()
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if !rec.Flushed || rec.Body.String() != "chunk" {
		t.Fatalf("flushed = %v, body = %q", rec.Flushed, rec.Body.String())
	}
}

// A writer without Flush support must not panic when a handler flushes.
type plainWriter struct{ header http.Header }

func (p plainWriter) Header() http.Header         { return p.header }
func (p plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p plainWriter) WriteHeader(int)             {}

func TestStatusRecorderFlushWithoutFlusher(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: plainWriter{header: http.Header{}}}
	rec.Flush()
}

func TestTracerAndInitTraces(t *testing.T) {
	if Tracer() == nil {
		t.Fatal("Tracer returned nil")
	}
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	tp := initTraces(buildResource("test"))
	if tp == nil {
		t.Fatal("initTraces returned no provider for a valid endpoint")
	}
	if otel.GetTracerProvider() != trace.TracerProvider(tp) {
		t.Error("initTraces did not register the tracer provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestBuildResourceHonorsServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "bugbarn-reader")
	res := buildResource("1.2.3")
	got := map[attribute.Key]string{}
	for _, kv := range res.Attributes() {
		got[kv.Key] = kv.Value.String()
	}
	if got["service.name"] != "bugbarn-reader" || got["service.version"] != "1.2.3" {
		t.Fatalf("resource = %v", got)
	}
	t.Setenv("OTEL_SERVICE_NAME", "")
	for _, kv := range buildResource("").Attributes() {
		if kv.Key == "service.name" && kv.Value.String() != "bugbarn" {
			t.Errorf("default service name = %q", kv.Value.String())
		}
		if kv.Key == "service.version" {
			t.Errorf("empty version should not set service.version")
		}
	}
}
