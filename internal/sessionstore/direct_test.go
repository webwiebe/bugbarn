package sessionstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/auth"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// fakeRefresher scripts the IdP token endpoint at the interface level:
// rotation on success, invalid_grant, or transient failure, with a call
// counter for singleflight assertions.
type fakeRefresher struct {
	mu       sync.Mutex
	calls    atomic.Int64
	delay    time.Duration
	err      error
	result   auth.RefreshedTokens
	denyNext bool
}

func (f *fakeRefresher) Refresh(_ context.Context, _ string) (auth.RefreshedTokens, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return auth.RefreshedTokens{}, f.err
	}
	return f.result, nil
}

func (f *fakeRefresher) Allowed(_ auth.Claims) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.denyNext
}

func newDirect(t *testing.T, oidc TokenRefresher) (*Direct, *storage.Store) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewDirect(db, oidc), db
}

func oidcSession(idHash string, now time.Time, accessExpires time.Time) storage.WebSession {
	return storage.WebSession{
		IDHash:            idHash,
		Username:          "alice",
		AuthMethod:        storage.WebSessionAuthOIDC,
		IdpSub:            "sub-1",
		IdpSid:            "sid-1",
		AccessToken:       "at-1",
		RefreshToken:      "rt-1",
		IDToken:           "idt-1",
		AccessExpiresAt:   accessExpires,
		CreatedAt:         now,
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}
}

func TestDirectCRUDAndNotFound(t *testing.T) {
	d, _ := newDirect(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := d.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	if err := d.Create(ctx, oidcSession("h1", now, now.Add(15*time.Minute))); err != nil {
		t.Fatal(err)
	}
	ws, err := d.Get(ctx, "h1")
	if err != nil || ws.Username != "alice" {
		t.Fatalf("Get = %+v, %v", ws, err)
	}
	if err := d.Delete(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete = %v", err)
	}
}

func TestDirectRefreshNotNeeded(t *testing.T) {
	fr := &fakeRefresher{}
	d, _ := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := d.Create(ctx, oidcSession("h1", now, now.Add(15*time.Minute))); err != nil {
		t.Fatal(err)
	}
	ws, err := d.Refresh(ctx, "h1")
	if err != nil || ws.AccessToken != "at-1" {
		t.Fatalf("fresh session refresh = %+v, %v", ws, err)
	}
	if fr.calls.Load() != 0 {
		t.Errorf("IdP must not be called for a fresh token, calls = %d", fr.calls.Load())
	}
}

func TestDirectRefreshSuccessRotatesAndResnapshots(t *testing.T) {
	fr := &fakeRefresher{result: auth.RefreshedTokens{
		AccessToken:  "at-2",
		RefreshToken: "rt-2",
		ExpiresAt:    time.Now().Add(15 * time.Minute),
		IDToken:      "idt-2",
		Claims:       &auth.Claims{Subject: "sub-1", PreferredUsername: "alice-renamed", Groups: []string{"g2"}},
	}}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	expired := oidcSession("h1", now.Add(-time.Hour), now.Add(-time.Minute))
	expired.RefreshFailingSince = now.Add(-10 * time.Minute)
	if err := db.InsertWebSession(ctx, expired); err != nil {
		t.Fatal(err)
	}

	ws, err := d.Refresh(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if ws.AccessToken != "at-2" || ws.RefreshToken != "rt-2" || ws.IDToken != "idt-2" {
		t.Errorf("tokens not rotated: %+v", ws)
	}
	if ws.Username != "alice-renamed" {
		t.Errorf("username not re-snapshotted: %q", ws.Username)
	}
	if !ws.RefreshFailingSince.IsZero() {
		t.Error("success must clear refresh_failing_since")
	}
	persisted, err := db.GetWebSession(ctx, "h1")
	if err != nil || persisted.RefreshToken != "rt-2" {
		t.Errorf("rotation not persisted: %+v, %v", persisted, err)
	}
}

func TestDirectRefreshInvalidGrantRevokes(t *testing.T) {
	fr := &fakeRefresher{err: auth.ErrRefreshInvalid}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.InsertWebSession(ctx, oidcSession("h1", now, now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Refresh(ctx, "h1"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("invalid_grant = %v, want ErrRevoked", err)
	}
	if _, err := d.Get(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Error("row must be deleted on invalid_grant")
	}
}

func TestDirectRefreshTransientSetsAnchorAndServesStale(t *testing.T) {
	fr := &fakeRefresher{err: fmt.Errorf("idp: 502")}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.InsertWebSession(ctx, oidcSession("h1", now, now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	ws, err := d.Refresh(ctx, "h1")
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("transient = %v, want ErrTransient", err)
	}
	if ws.AccessToken != "at-1" || ws.RefreshFailingSince.IsZero() {
		t.Errorf("stale row missing/unmarked: %+v", ws)
	}
	persisted, _ := db.GetWebSession(ctx, "h1")
	first := persisted.RefreshFailingSince
	if first.IsZero() {
		t.Fatal("refresh_failing_since not persisted")
	}
	// A second failure keeps the original outage anchor.
	if _, err := d.Refresh(ctx, "h1"); !errors.Is(err, ErrTransient) {
		t.Fatal(err)
	}
	persisted, _ = db.GetWebSession(ctx, "h1")
	if !persisted.RefreshFailingSince.Equal(first) {
		t.Errorf("anchor moved from %v to %v", first, persisted.RefreshFailingSince)
	}
}

func TestDirectRefreshWithoutRefreshTokenRevokes(t *testing.T) {
	d, db := newDirect(t, &fakeRefresher{})
	ctx := context.Background()
	now := time.Now().UTC()
	ws := oidcSession("h1", now, now.Add(-time.Minute))
	ws.RefreshToken = ""
	if err := db.InsertWebSession(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Refresh(ctx, "h1"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("no-refresh-token = %v, want ErrRevoked", err)
	}
}

func TestDirectRefreshAccessLostRevokes(t *testing.T) {
	fr := &fakeRefresher{
		denyNext: true,
		result: auth.RefreshedTokens{
			AccessToken:  "at-2",
			RefreshToken: "rt-2",
			ExpiresAt:    time.Now().Add(15 * time.Minute),
			Claims:       &auth.Claims{Subject: "sub-1"}, // no longer in the required group
		},
	}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.InsertWebSession(ctx, oidcSession("h1", now, now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Refresh(ctx, "h1"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("access lost = %v, want ErrRevoked", err)
	}
	if _, err := d.Get(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Error("row must be deleted when central access is revoked")
	}
}

func TestDirectRefreshSingleflight(t *testing.T) {
	fr := &fakeRefresher{
		delay: 50 * time.Millisecond,
		result: auth.RefreshedTokens{
			AccessToken:  "at-2",
			RefreshToken: "rt-2",
			ExpiresAt:    time.Now().Add(15 * time.Minute),
		},
	}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.InsertWebSession(ctx, oidcSession("h1", now, now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := d.Refresh(ctx, "h1"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// The single-use refresh token must have been exchanged exactly once.
	if got := fr.calls.Load(); got != 1 {
		t.Errorf("refresh calls = %d, want 1 (singleflight)", got)
	}
}

func TestDirectLocalSessionsNeverRefresh(t *testing.T) {
	fr := &fakeRefresher{}
	d, db := newDirect(t, fr)
	ctx := context.Background()
	now := time.Now().UTC()
	local := storage.WebSession{
		IDHash:            "h-local",
		Username:          "admin",
		AuthMethod:        storage.WebSessionAuthLocal,
		CreatedAt:         now,
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}
	if err := db.InsertWebSession(ctx, local); err != nil {
		t.Fatal(err)
	}
	if NeedsRefresh(local, now) {
		t.Error("local sessions must not need refresh")
	}
	ws, err := d.Refresh(ctx, "h-local")
	if err != nil || ws.Username != "admin" {
		t.Fatalf("local refresh passthrough = %+v, %v", ws, err)
	}
	if fr.calls.Load() != 0 {
		t.Error("local session must not hit the IdP")
	}
}

func TestDirectDeleteBySIDAndSub(t *testing.T) {
	d, db := newDirect(t, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.InsertWebSession(ctx, oidcSession("h1", now, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	other := oidcSession("h2", now, now.Add(time.Hour))
	other.IdpSid = "sid-other"
	if err := db.InsertWebSession(ctx, other); err != nil {
		t.Fatal(err)
	}
	if n, err := d.DeleteBySID(ctx, "sid-1"); err != nil || n != 1 {
		t.Fatalf("DeleteBySID = %d, %v", n, err)
	}
	if n, err := d.DeleteBySub(ctx, "sub-1"); err != nil || n != 1 {
		t.Fatalf("DeleteBySub = %d, %v", n, err)
	}
}
