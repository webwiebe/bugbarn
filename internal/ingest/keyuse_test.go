package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

const leakedKey = "leaked-key-value"

// storedKeyHandler authenticates leakedKey as api_keys row 40, bound to project 9.
func storedKeyHandler(t *testing.T, buf *bytes.Buffer) *Handler {
	t.Helper()
	sum := sha256.Sum256([]byte(leakedKey))
	want := hex.EncodeToString(sum[:])
	lookup := func(_ context.Context, sha string) (int64, int64, string, bool, error) {
		if sha == want {
			return 40, 9, "ingest", true, nil
		}
		return 0, 0, "", false, nil
	}
	h := NewHandler(auth.New("static").WithDBIdentityLookup(lookup, nil), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(buf, nil))
	return h
}

func keyUseRequest(key, ua string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(`{}`))
	req.Header.Set(auth.HeaderAPIKey, key)
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = "203.0.113.7:5000"
	return req
}

func TestAcceptedKeyLogsIdentityWithoutKeyOrHash(t *testing.T) {
	var buf bytes.Buffer
	h := storedKeyHandler(t, &buf)

	h.CheckAPIKey(keyUseRequest(leakedKey, "probe/1.0"))

	sum := sha256.Sum256([]byte(leakedKey))
	out := buf.String()
	for _, want := range []string{"level=INFO", "key_id=40", "project_id=9", "scope=ingest", "ua=probe/1.0", "ip=203.0.113.7", "path=/api/v1/events"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q lacks %q", out, want)
		}
	}
	if strings.Contains(out, leakedKey) || strings.Contains(out, hex.EncodeToString(sum[:])[:6]) {
		t.Fatalf("log leaks the key or its hash: %q", out)
	}
}

func TestKeyUseLogIsThrottledPerKeyAndUserAgent(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	h := storedKeyHandler(t, &buf)
	h.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		h.CheckAPIKey(keyUseRequest(leakedKey, "probe/1.0"))
	}
	if n := strings.Count(buf.String(), "api key used"); n != 1 {
		t.Fatalf("logged %d times within the window, want 1", n)
	}

	h.CheckAPIKey(keyUseRequest(leakedKey, "other-agent/2"))
	if n := strings.Count(buf.String(), "api key used"); n != 2 {
		t.Fatalf("a second user agent logged %d lines in total, want 2", n)
	}

	now = now.Add(keyUseLogEvery + time.Second)
	h.CheckAPIKey(keyUseRequest(leakedKey, "probe/1.0"))
	if n := strings.Count(buf.String(), "api key used"); n != 3 {
		t.Fatalf("logged %d lines after the window, want 3", n)
	}
}

func TestKeyUseTableResetsWhenFull(t *testing.T) {
	var buf bytes.Buffer
	h := storedKeyHandler(t, &buf)
	for i := 0; i < keyUseMaxCallers+5; i++ {
		h.observeKeyUse(keyUseRequest(leakedKey, "agent-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+hex.EncodeToString([]byte{byte(i), byte(i >> 8)})), auth.KeyIdentity{ID: int64(i)})
	}
	if len(h.keyUseAt) > keyUseMaxCallers {
		t.Fatalf("throttle table holds %d callers, limit %d", len(h.keyUseAt), keyUseMaxCallers)
	}
}

func TestRejectedKeyIsNotLoggedAsUsed(t *testing.T) {
	var buf bytes.Buffer
	h := storedKeyHandler(t, &buf)

	h.CheckAPIKey(keyUseRequest("wrong", "probe/1.0"))

	if strings.Contains(buf.String(), "api key used") {
		t.Fatalf("rejected key logged as used: %q", buf.String())
	}
}

func TestKeyUseRecordsIDAndUserAgentOnTheSpan(t *testing.T) {
	var buf bytes.Buffer
	h := storedKeyHandler(t, &buf)
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, span := tp.Tracer("test").Start(context.Background(), "server", trace.WithSpanKind(trace.SpanKindServer))

	h.CheckAPIKey(keyUseRequest(leakedKey, "probe/1.0").WithContext(ctx))
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	got := map[string]string{}
	for _, kv := range spans[0].Attributes {
		got[string(kv.Key)] = kv.Value.String()
	}
	if got["bugbarn.caller.api_key_id"] != "40" {
		t.Errorf("api_key_id = %q, want 40", got["bugbarn.caller.api_key_id"])
	}
	if got["bugbarn.caller.user_agent"] != "probe/1.0" {
		t.Errorf("user_agent = %q, want probe/1.0", got["bugbarn.caller.user_agent"])
	}
}

func TestKeyUseTruncatesLongUserAgentAndUsesClientIPHook(t *testing.T) {
	var buf bytes.Buffer
	h := storedKeyHandler(t, &buf)
	h.SetClientIP(func(*http.Request) string { return "198.51.100.4" })

	h.CheckAPIKey(keyUseRequest(leakedKey, strings.Repeat("a", 500)))

	out := buf.String()
	if !strings.Contains(out, "ip=198.51.100.4") {
		t.Fatalf("log %q lacks the hooked client ip", out)
	}
	if strings.Contains(out, strings.Repeat("a", maxUserAgentLen+1)) {
		t.Fatalf("user agent not truncated: %q", out)
	}
}

func TestCallerIPFallsBackToRawRemoteAddr(t *testing.T) {
	h := NewHandler(auth.New("x"), mustSpool(t), 1024)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "no-port"
	if got := h.callerIP(req); got != "no-port" {
		t.Fatalf("callerIP = %q, want the raw address", got)
	}
	var nilHandler *Handler
	nilHandler.SetClientIP(nil)
}
