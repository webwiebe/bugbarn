package api

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
)

// recordSpan runs fn inside a real recording span and returns its attributes.
func recordSpan(t *testing.T, fn func(ctx context.Context)) map[attribute.Key]attribute.Value {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	ctx, span := tp.Tracer("test").Start(context.Background(), "server", trace.WithSpanKind(trace.SpanKindServer))
	fn(ctx)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	attrs := make(map[attribute.Key]attribute.Value, len(spans[0].Attributes))
	for _, kv := range spans[0].Attributes {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

// Without this, every Go caller is indistinguishable: they all send the same
// "Go-http-client/2.0" user agent.
func TestAnnotateCallerRecordsAPIKeyIdentity(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	attrs := recordSpan(t, func(ctx context.Context) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil).WithContext(ctx)
		req.RemoteAddr = "203.0.113.9:44120"
		server.annotateCaller(req, false, true, 61)
	})

	if got := attrs["bugbarn.caller.auth"].AsString(); got != "api_key" {
		t.Errorf("bugbarn.caller.auth = %q, want %q", got, "api_key")
	}
	if got := attrs["bugbarn.caller.ip"].AsString(); got != "203.0.113.9" {
		t.Errorf("bugbarn.caller.ip = %q, want %q", got, "203.0.113.9")
	}
	if got := attrs["bugbarn.caller.api_key_project_id"].AsInt64(); got != 61 {
		t.Errorf("bugbarn.caller.api_key_project_id = %d, want 61", got)
	}
}

func TestAnnotateCallerRecordsSessionIdentity(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	attrs := recordSpan(t, func(ctx context.Context) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil).WithContext(ctx)
		req.RemoteAddr = "198.51.100.4:9000"
		server.annotateCaller(req, true, false, 0)
	})

	if got := attrs["bugbarn.caller.auth"].AsString(); got != "session" {
		t.Errorf("bugbarn.caller.auth = %q, want %q", got, "session")
	}
	// A session caller has no API-key project, so the attribute must be absent
	// rather than reported as project 0 (which is a real, meaningful id).
	if _, present := attrs["bugbarn.caller.api_key_project_id"]; present {
		t.Error("api_key_project_id recorded for a session-authenticated caller")
	}
}

// The API key itself must never reach a span — spans are shipped off-box.
func TestAnnotateCallerNeverRecordsTheKey(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	const secret = "super-secret-key-value"
	attrs := recordSpan(t, func(ctx context.Context) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil).WithContext(ctx)
		req.Header.Set("X-BugBarn-API-Key", secret)
		server.annotateCaller(req, false, true, 7)
	})

	for k, v := range attrs {
		if v.AsString() == secret {
			t.Errorf("attribute %s leaked the API key", k)
		}
	}
}

func TestAnnotateCallerNoopWithoutRecordingSpan(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	server := NewServer(nil, store, nil)

	// No span in context: must not panic.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	server.annotateCaller(req, true, false, 0)
}

// The key-use log line names the caller the way the span does: through the
// trusted-proxy aware resolver the server hands to the ingest handler.
func TestSetTrustedProxiesGivesTheIngestHandlerTheClientIPResolver(t *testing.T) {
	store := mustOpenStore(t)
	defer store.Close()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	handler := ingest.NewHandler(auth.New("secret"), nil, 1024)
	slog.SetDefault(prev)
	server := NewServer(handler, store, nil)
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	server.SetTrustedProxies([]*net.IPNet{cidr})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", nil)
	req.RemoteAddr = "10.1.2.3:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, 10.1.2.3")
	req.Header.Set(auth.HeaderAPIKey, "secret")
	handler.CheckAPIKey(req)

	if !strings.Contains(logs.String(), "ip=198.51.100.77") {
		t.Fatalf("log %q lacks the forwarded client ip", logs.String())
	}
}
