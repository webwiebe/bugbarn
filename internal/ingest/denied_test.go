package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
)

func deniedRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logs", strings.NewReader(`{}`))
	req.Header.Set("User-Agent", "probe/1.0")
	req.Header.Set("X-BugBarn-Project", "web")
	if key != "" {
		req.Header.Set(auth.HeaderAPIKey, key)
	}
	return req
}

func TestDeniedKeyLogsCallerWithoutTheKey(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	h.APIKeyProjectScope(deniedRequest("wrong-key-value"))

	sum := sha256.Sum256([]byte("wrong-key-value"))
	out := buf.String()
	for _, want := range []string{"level=WARN", "key=" + hex.EncodeToString(sum[:])[:6], "ua=probe/1.0", "project=web", "path=/api/v1/logs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q lacks %q", out, want)
		}
	}
	if strings.Contains(out, "wrong-key-value") {
		t.Fatalf("log leaks the key: %q", out)
	}
}

func TestDeniedMissingKeyIsNamedMissing(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	h.APIKeyProjectScope(deniedRequest(""))

	if !strings.Contains(buf.String(), "key=missing") {
		t.Fatalf("log %q lacks key=missing", buf.String())
	}
}

func TestDeniedLogIsThrottledPerCaller(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))
	h.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		h.APIKeyProjectScope(deniedRequest("wrong-key-value"))
	}
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("logged %d times within the window, want 1", n)
	}

	now = now.Add(deniedLogEvery + time.Second)
	h.APIKeyProjectScope(deniedRequest("wrong-key-value"))
	if n := strings.Count(buf.String(), "level=WARN"); n != 2 {
		t.Fatalf("logged %d times after the window, want 2", n)
	}
}

func TestAcceptedKeyIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(auth.New("secret"), mustSpool(t), 1024)
	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	h.APIKeyProjectScope(deniedRequest("secret"))

	if buf.Len() != 0 {
		t.Fatalf("accepted key logged: %q", buf.String())
	}
}
