package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/keytouch"
	"github.com/wiebe-xyz/bugbarn/internal/sessionstore"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const keyTouchSecret = "touch-secret"

func newKeyTouchWriter(t *testing.T) (*storage.Store, *httptest.Server, domain.APIKey) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	proj, err := store.CreateProject(context.Background(), "Test", "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(context.Background(), "k", proj.ID, "hash-k", storage.APIKeyScopeIngest)
	if err != nil {
		t.Fatal(err)
	}
	userAuth, err := auth.NewUserAuthenticator("admin", "change-me", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewServerWithAuth(nil, store, userAuth, auth.NewSessionManager(keyTouchSecret, 12*time.Hour), nil, nil)
	writer.SetInternalSessionSecret(keyTouchSecret)
	writer.SetKeyToucher(store)
	srv := httptest.NewServer(writer)
	t.Cleanup(srv.Close)
	return store, srv, key
}

func lastUsed(t *testing.T, store *storage.Store, id int64) time.Time {
	t.Helper()
	keys, err := store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.ID == id {
			return k.LastUsedAt
		}
	}
	t.Fatalf("key %d not found", id)
	return time.Time{}
}

// A reader's batcher reaches the writer through the real endpoint and the key's
// last_used_at moves, which the reader's own read-only store cannot do.
func TestReaderTouchesReachTheWriter(t *testing.T) {
	store, srv, key := newKeyTouchWriter(t)
	if !lastUsed(t, store, key.ID).IsZero() {
		t.Fatal("a new key starts never used")
	}

	batcher := keytouch.NewBatcher(keytouch.NewRemoteSender(srv.URL, keyTouchSecret), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := batcher.Touch(context.Background(), key.KeySHA256); err != nil {
		t.Fatal(err)
	}
	if err := batcher.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(t, store, key.ID); time.Since(got) > time.Minute || got.IsZero() {
		t.Fatalf("last_used_at = %v, want about now", got)
	}
}

func TestKeyTouchEndpointRejectsUnsignedAndStaleBatches(t *testing.T) {
	store, srv, key := newKeyTouchWriter(t)
	post := func(body []byte, signature string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+keytouch.Path, bytes.NewReader(body))
		if signature != "" {
			req.Header.Set(sessionstore.AuthHeader, signature)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	batch := func(ts int64) []byte {
		b, _ := json.Marshal(keytouch.Batch{TS: ts, Touches: []domain.APIKeyTouch{{KeySHA256: key.KeySHA256, At: time.Now()}}})
		return b
	}

	fresh := batch(time.Now().Unix())
	if got := post(fresh, ""); got != http.StatusForbidden {
		t.Fatalf("unsigned = %d, want 403", got)
	}
	if got := post(fresh, sessionstore.SignBody([]byte("wrong-secret"), fresh)); got != http.StatusForbidden {
		t.Fatalf("wrong secret = %d, want 403", got)
	}
	stale := batch(time.Now().Add(-time.Hour).Unix())
	if got := post(stale, sessionstore.SignBody([]byte(keyTouchSecret), stale)); got != http.StatusForbidden {
		t.Fatalf("stale timestamp = %d, want 403", got)
	}
	if !lastUsed(t, store, key.ID).IsZero() {
		t.Fatal("a rejected batch must not touch the key")
	}
	if got := post(fresh, sessionstore.SignBody([]byte(keyTouchSecret), fresh)); got != http.StatusOK {
		t.Fatalf("signed fresh batch = %d, want 200", got)
	}
}
