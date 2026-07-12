package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestSignAndVerifyBody(t *testing.T) {
	secret := []byte("shared-secret")
	body := []byte(`{"ts":1,"id_hash":"h"}`)
	sig := SignBody(secret, body)
	if !VerifyBody(secret, body, sig) {
		t.Fatal("valid signature rejected")
	}
	if VerifyBody(secret, []byte(`{"ts":2}`), sig) {
		t.Error("tampered body accepted")
	}
	if VerifyBody([]byte("other"), body, sig) {
		t.Error("wrong secret accepted")
	}
	if VerifyBody(secret, body, "") || VerifyBody(nil, body, sig) {
		t.Error("empty header/secret must fail closed")
	}
}

func TestFreshTS(t *testing.T) {
	now := time.Now()
	if !FreshTS(now.Unix(), now) || !FreshTS(now.Add(-time.Minute).Unix(), now) {
		t.Error("recent timestamps must be fresh")
	}
	if FreshTS(now.Add(-10*time.Minute).Unix(), now) || FreshTS(now.Add(10*time.Minute).Unix(), now) {
		t.Error("stale/future timestamps must be rejected")
	}
	if FreshTS(0, now) {
		t.Error("zero timestamp must be rejected")
	}
}

// newRemoteHarness returns a Remote store whose local reads hit a real
// read-only SQLite and whose writer is the given handler.
func newRemoteHarness(t *testing.T, writer http.HandlerFunc) (*Remote, *storage.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	rw, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rw.Close() })
	ro, err := storage.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })
	srv := httptest.NewServer(writer)
	t.Cleanup(srv.Close)
	return NewRemote(ro, srv.URL, "shared-secret"), rw
}

func TestRemoteGetReadsLocalSQLite(t *testing.T) {
	remote, rw := newRemoteHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("plain validation reads must not hit the writer")
	})
	ctx := context.Background()
	now := time.Now().UTC()
	if err := rw.InsertWebSession(ctx, oidcSession("h1", now, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	ws, err := remote.Get(ctx, "h1")
	if err != nil || ws.Username != "alice" {
		t.Fatalf("Get = %+v, %v", ws, err)
	}
	if _, err := remote.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v", err)
	}
}

func TestRemoteCallSigningAndStatusMapping(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	status := http.StatusOK
	respond := func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get(AuthHeader)
		gotBody, _ = io.ReadAll(r.Body)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		ws := oidcSession("h1", time.Now().UTC(), time.Now().Add(time.Hour))
		_ = json.NewEncoder(w).Encode(Response{Status: StatusOK, Session: &ws})
	}
	remote, _ := newRemoteHarness(t, respond)
	ctx := context.Background()

	ws, err := remote.Refresh(ctx, "h1")
	if err != nil || ws.IDHash != "h1" {
		t.Fatalf("Refresh ok = %+v, %v", ws, err)
	}
	// The request must be HMAC-signed over the exact body with a fresh ts.
	if !VerifyBody([]byte("shared-secret"), gotBody, gotAuth) {
		t.Error("request body signature invalid")
	}
	var req Request
	if err := json.Unmarshal(gotBody, &req); err != nil || req.IDHash != "h1" || !FreshTS(req.TS, time.Now()) {
		t.Errorf("request payload = %+v, %v", req, err)
	}

	status = http.StatusUnauthorized
	if _, err := remote.Refresh(ctx, "h1"); !errors.Is(err, ErrRevoked) {
		t.Errorf("401 = %v, want ErrRevoked", err)
	}
	status = http.StatusNotFound
	if _, err := remote.Refresh(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v, want ErrNotFound", err)
	}
	status = http.StatusBadGateway
	if _, err := remote.Refresh(ctx, "h1"); !errors.Is(err, ErrTransient) {
		t.Errorf("502 = %v, want ErrTransient", err)
	}
}

func TestRemoteTransientCarriesStaleSession(t *testing.T) {
	remote, _ := newRemoteHarness(t, func(w http.ResponseWriter, r *http.Request) {
		ws := oidcSession("h1", time.Now().UTC(), time.Now().Add(-time.Minute))
		ws.RefreshFailingSince = time.Now().UTC().Add(-2 * time.Minute)
		_ = json.NewEncoder(w).Encode(Response{Status: StatusTransient, Error: "idp 502", Session: &ws})
	})
	ws, err := remote.Refresh(context.Background(), "h1")
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient", err)
	}
	if ws.IDHash != "h1" || ws.RefreshFailingSince.IsZero() {
		t.Errorf("stale session = %+v", ws)
	}
}

func TestRemoteWriterUnreachableIsTransient(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	rw, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	remote := NewRemote(rw, "http://127.0.0.1:1", "s")
	if _, err := remote.Refresh(context.Background(), "h1"); !errors.Is(err, ErrTransient) {
		t.Errorf("unreachable writer = %v, want ErrTransient", err)
	}
	if err := remote.Create(context.Background(), storage.WebSession{IDHash: "x"}); !errors.Is(err, ErrTransient) {
		t.Errorf("unreachable create = %v, want ErrTransient", err)
	}
}

func TestRemoteDeleteOperations(t *testing.T) {
	var ops []string
	remote, _ := newRemoteHarness(t, func(w http.ResponseWriter, r *http.Request) {
		ops = append(ops, r.URL.Path)
		_ = json.NewEncoder(w).Encode(Response{Status: StatusOK, Deleted: 2})
	})
	ctx := context.Background()
	if err := remote.Delete(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if n, err := remote.DeleteBySID(ctx, "sid-1"); err != nil || n != 2 {
		t.Fatalf("DeleteBySID = %d, %v", n, err)
	}
	if n, err := remote.DeleteBySub(ctx, "sub-1"); err != nil || n != 2 {
		t.Fatalf("DeleteBySub = %d, %v", n, err)
	}
	want := []string{
		InternalPathPrefix + "delete",
		InternalPathPrefix + "delete-by-sid",
		InternalPathPrefix + "delete-by-sub",
	}
	for i, p := range want {
		if ops[i] != p {
			t.Errorf("op %d = %q, want %q", i, ops[i], p)
		}
	}
}
