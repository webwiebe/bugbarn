package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func newWebSessionStore(t *testing.T) (*Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "bugbarn.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, dbPath
}

func sampleWebSession(idHash string, now time.Time) WebSession {
	return WebSession{
		IDHash:            idHash,
		Username:          "alice",
		AuthMethod:        WebSessionAuthOIDC,
		IdpSub:            "sub-1",
		IdpSid:            "sid-1",
		IDToken:           "idt",
		AccessToken:       "at",
		RefreshToken:      "rt",
		AccessExpiresAt:   now.Add(15 * time.Minute),
		ClaimsJSON:        `{"sub":"sub-1"}`,
		CreatedAt:         now,
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}
}

func TestWebSessionCRUD(t *testing.T) {
	store, _ := newWebSessionStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	ws := sampleWebSession("hash-1", now)
	if err := store.InsertWebSession(ctx, ws); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetWebSession(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "alice" || got.IdpSid != "sid-1" || got.RefreshToken != "rt" {
		t.Errorf("got %+v", got)
	}
	if !got.AccessExpiresAt.Equal(ws.AccessExpiresAt) || !got.AbsoluteExpiresAt.Equal(ws.AbsoluteExpiresAt) {
		t.Errorf("times mismatched: %+v", got)
	}
	if !got.LastRefreshAt.IsZero() || !got.RefreshFailingSince.IsZero() {
		t.Errorf("optional times should round-trip as zero: %+v", got)
	}

	if _, err := store.GetWebSession(ctx, "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("missing row: got %v, want not_found", err)
	}

	// Refresh outcome: tokens rotate, claims re-snapshot, failure marker clears.
	if err := store.MarkWebSessionRefreshFailing(ctx, "hash-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got.AccessToken, got.RefreshToken, got.IDToken = "at2", "rt2", "idt2"
	got.AccessExpiresAt = now.Add(30 * time.Minute)
	got.LastRefreshAt = now.Add(16 * time.Minute)
	got.ClaimsJSON = `{"sub":"sub-1","groups":["g2"]}`
	if err := store.UpdateWebSessionTokens(ctx, got); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetWebSession(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RefreshToken != "rt2" || updated.ClaimsJSON != `{"sub":"sub-1","groups":["g2"]}` {
		t.Errorf("update not applied: %+v", updated)
	}
	if !updated.RefreshFailingSince.IsZero() {
		t.Error("successful update must clear refresh_failing_since")
	}
	if updated.LastRefreshAt.IsZero() {
		t.Error("last_refresh_at should be set")
	}

	if err := store.UpdateWebSessionTokens(ctx, WebSession{IDHash: "missing"}); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("update of missing row: got %v, want not_found", err)
	}

	if err := store.DeleteWebSession(ctx, "hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetWebSession(ctx, "hash-1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("deleted row still readable: %v", err)
	}
}

func TestWebSessionRefreshFailingKeepsFirstAnchor(t *testing.T) {
	store, _ := newWebSessionStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := store.InsertWebSession(ctx, sampleWebSession("hash-f", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWebSessionRefreshFailing(ctx, "hash-f", now); err != nil {
		t.Fatal(err)
	}
	// A later failure must NOT move the anchor — retries can't extend grace.
	if err := store.MarkWebSessionRefreshFailing(ctx, "hash-f", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetWebSession(ctx, "hash-f")
	if err != nil {
		t.Fatal(err)
	}
	if !got.RefreshFailingSince.Equal(now) {
		t.Errorf("anchor moved: %v, want %v", got.RefreshFailingSince, now)
	}
}

func TestWebSessionBulkDeletesAndPrune(t *testing.T) {
	store, _ := newWebSessionStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	a := sampleWebSession("hash-a", now)
	b := sampleWebSession("hash-b", now)
	b.IdpSid = "sid-2"
	c := sampleWebSession("hash-c", now)
	c.IdpSub, c.IdpSid = "sub-2", "sid-3"
	expired := sampleWebSession("hash-old", now.Add(-24*time.Hour))
	expired.IdpSub, expired.IdpSid = "sub-3", "sid-4"
	expired.AbsoluteExpiresAt = now.Add(-12 * time.Hour)
	for _, ws := range []WebSession{a, b, c, expired} {
		if err := store.InsertWebSession(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}

	if n, err := store.DeleteWebSessionsBySID(ctx, "sid-1"); err != nil || n != 1 {
		t.Fatalf("DeleteBySID = %d, %v", n, err)
	}
	if n, err := store.DeleteWebSessionsBySub(ctx, "sub-1"); err != nil || n != 1 {
		t.Fatalf("DeleteBySub = %d, %v (should only match hash-b)", n, err)
	}
	// Empty selectors must never mass-delete.
	if n, err := store.DeleteWebSessionsBySID(ctx, ""); err != nil || n != 0 {
		t.Fatalf("DeleteBySID(\"\") = %d, %v", n, err)
	}
	if n, err := store.PruneWebSessions(ctx, now); err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	if _, err := store.GetWebSession(ctx, "hash-c"); err != nil {
		t.Errorf("unexpired unrelated session must survive: %v", err)
	}
}

func TestWebSessionReadOnlyStore(t *testing.T) {
	store, dbPath := newWebSessionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.InsertWebSession(ctx, sampleWebSession("hash-ro", now)); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	// Reads work on the read-only mount (the CQRS readers' validation path)…
	got, err := ro.GetWebSession(ctx, "hash-ro")
	if err != nil || got.Username != "alice" {
		t.Fatalf("read-only get: %+v, %v", got, err)
	}
	// …while writes are refused instead of failing open.
	if err := ro.InsertWebSession(ctx, sampleWebSession("hash-x", now)); err == nil {
		t.Error("insert on read-only store must fail")
	}
	if err := ro.DeleteWebSession(ctx, "hash-ro"); err == nil {
		t.Error("delete on read-only store must fail")
	}
}
