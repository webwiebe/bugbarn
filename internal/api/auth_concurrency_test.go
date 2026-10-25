package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// A key lookup that fails under load (a busy read pool, a locked database, a
// request deadline) says nothing about the key. profotograaf's workers check in
// about thirty at a time and a valid key was answered 401 during the burst; a
// valid key must get 429 or 503 with Retry-After when the lookup cannot run,
// never a permanent 401 that tells the sender to stop.
func TestValidKeyIsNeverUnauthorizedWhenTheLookupFailsUnderLoad(t *testing.T) {
	t.Parallel()
	store := mustOpenStore(t)
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	proj, err := store.CreateProject(ctx, "geo", "geo")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("geo-key"))
	if _, err := store.CreateAPIKey(ctx, "geo-ingest", proj.ID, hex.EncodeToString(sum[:]), storage.APIKeyScopeIngest); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64
	flaky := func(ctx context.Context, sha string) (int64, string, bool, error) {
		if calls.Add(1)%3 == 0 {
			return 0, "", false, errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return store.ValidAPIKeySHA256(ctx, sha)
	}
	authorizer := auth.New("unbound-key").WithDBLookup(flaky, store.TouchAPIKey)
	userAuth, _ := auth.NewUserAuthenticator("admin", "pass", "")
	server := NewServerWithAuth(ingest.NewHandler(authorizer, nil, 1<<20), store, userAuth,
		auth.NewSessionManager("secret", time.Hour), nil, nil)

	const burst = 60
	codes := make([]int, burst)
	retryAfter := make([]string, burst)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/monitors/worker-"+string(rune('a'+i%26))+"/check-ins",
				strings.NewReader(`{"status":"ok","interval":"1h"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-BugBarn-API-Key", "geo-key")
			rr := httptest.NewRecorder()
			<-start
			server.ServeHTTP(rr, req)
			codes[i] = rr.Code
			retryAfter[i] = rr.Header().Get("Retry-After")
		}()
	}
	close(start)
	wg.Wait()

	var ok, unavailable int
	for i, code := range codes {
		switch code {
		case http.StatusOK, http.StatusCreated:
			ok++
		case http.StatusServiceUnavailable, http.StatusTooManyRequests:
			unavailable++
			if retryAfter[i] == "" {
				t.Errorf("response %d: %d without Retry-After", i, code)
			}
		default:
			t.Errorf("response %d: valid key answered %d, want 2xx or 429/503 with Retry-After", i, code)
		}
	}
	if ok == 0 || unavailable == 0 {
		t.Fatalf("the failing lookup was not exercised: ok=%d unavailable=%d", ok, unavailable)
	}
}

// A key that is genuinely unknown still gets the permanent 401 when the lookup
// itself works.
func TestUnknownKeyIsStillUnauthorizedWhenTheLookupWorks(t *testing.T) {
	t.Parallel()
	h := newMonitorHarness(t)
	if rr := h.post("worker", "no-such-key", `{"status":"ok","interval":"1h"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rr.Code)
	}
}
