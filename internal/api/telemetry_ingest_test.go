package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/telemetry"
)

type fakeTelemetry struct {
	mu    sync.Mutex
	kinds []string
	err   error
}

func (f *fakeTelemetry) Ingest(_ context.Context, kind string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	return f.err
}

func createKey(t *testing.T, store *storage.Store, projectID int64, scope string) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	plain := hex.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(plain))
	if _, err := store.CreateAPIKey(context.Background(), "k-"+plain[:6], projectID, hex.EncodeToString(sum[:]), scope); err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestTelemetryIngestEndpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := mustOpenStore(t)
	defer store.Close()

	infra, err := store.CreateProject(ctx, "Infra", "infra")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.ProjectBySlug(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	infraIngest := createKey(t, store, infra.ID, storage.APIKeyScopeIngest)
	otherIngest := createKey(t, store, other.ID, storage.APIKeyScopeIngest)
	full := createKey(t, store, other.ID, storage.APIKeyScopeFull)

	authorizer := auth.New("").WithDBLookup(store.ValidAPIKeySHA256, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<10), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)
	tel := &fakeTelemetry{}
	server.SetTelemetry(tel, "infra")

	post := func(method, path, key, body string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			req.Header.Set("x-bugbarn-api-key", key)
		}
		server.ServeHTTP(rr, req)
		return rr.Code
	}

	cases := []struct {
		name, method, path, key, body string
		want                          int
	}{
		{"infra ingest key, security", http.MethodPost, telemetrySecurityPath, infraIngest, "{}", http.StatusAccepted},
		{"infra ingest key, metrics", http.MethodPost, telemetryMetricsPath, infraIngest, "{}", http.StatusAccepted},
		{"full key of any project", http.MethodPost, telemetrySecurityPath, full, "{}", http.StatusAccepted},
		{"ingest key of another project", http.MethodPost, telemetrySecurityPath, otherIngest, "{}", http.StatusUnauthorized},
		{"no key", http.MethodPost, telemetrySecurityPath, "", "{}", http.StatusUnauthorized},
		{"PUT", http.MethodPut, telemetrySecurityPath, infraIngest, "", http.StatusMethodNotAllowed},
		{"body over the limit", http.MethodPost, telemetrySecurityPath, infraIngest, strings.Repeat("x", 2<<10), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if got := post(c.method, c.path, c.key, c.body); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	tel.mu.Lock()
	kinds := strings.Join(tel.kinds, ",")
	tel.mu.Unlock()
	if kinds != "security,metrics,security" {
		t.Fatalf("ingested kinds = %q", kinds)
	}

	// A failed insert is still accepted: detections already ran.
	tel.err = context.DeadlineExceeded
	if got := post(http.MethodPost, telemetrySecurityPath, infraIngest, "{}"); got != http.StatusAccepted {
		t.Errorf("insert error: status %d, want 202", got)
	}
	// A file that never opened asks the sender to retry.
	tel.err = telemetry.ErrUnavailable
	if got := post(http.MethodPost, telemetrySecurityPath, infraIngest, "{}"); got != http.StatusServiceUnavailable {
		t.Errorf("unavailable: status %d, want 503", got)
	}
	server.SetTelemetry(nil, "infra")
	if got := post(http.MethodPost, telemetryMetricsPath, infraIngest, "{}"); got != http.StatusServiceUnavailable {
		t.Errorf("no ingester: status %d, want 503", got)
	}
}

func TestKindForPathTelemetry(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		telemetrySecurityPath:          queue.KindSecurity,
		telemetryMetricsPath + "?x=1":  queue.KindMetrics,
		"/api/v1/telemetry/other":      "",
		"/api/v1/telemetry/security/x": "",
	} {
		if got := kindForPath(path); got != want {
			t.Errorf("kindForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// Readers spool telemetry into the Redis queue with its own kind; the
// consumer routes on kind and needs no project slug.
func TestRedisSpoolForwarderTelemetry(t *testing.T) {
	sf, q := newTestRedisForwarder(t)
	req := httptest.NewRequest(http.MethodPost, telemetrySecurityPath, strings.NewReader("LINE"))
	rec := httptest.NewRecorder()
	sf.Forward(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("Forward status = %d, want 202", rec.Code)
	}
	if err := sf.DrainOnce(context.Background()); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	items, err := q.Consume(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != queue.KindSecurity {
		t.Fatalf("items = %+v, want one security item", items)
	}
}
