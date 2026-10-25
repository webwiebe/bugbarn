package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
	"github.com/wiebe-xyz/bugbarn/internal/fingerprint"
	"github.com/wiebe-xyz/bugbarn/internal/worker"
)

// openForMigration opens a store with the background migration off, so each
// test decides when migrateFingerprints runs.
func openForMigration(t *testing.T) *Store {
	t.Helper()
	store, err := open(filepath.Join(t.TempDir(), "bugbarn.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func migrationEvent(observed time.Time) event.Event {
	return event.Event{
		ObservedAt: observed,
		ReceivedAt: observed.Add(time.Second),
		Severity:   "ERROR",
		Message:    "HighErrorRate",
		Exception:  event.Exception{Type: "alert", Message: "HighErrorRate"},
	}
}

// withFingerprint builds the ProcessedEvent the ingest path produces when the
// event carries fp: the material and explanation are still derived from the
// event, exactly as worker.ProcessRecord does for an SDK override.
func withFingerprint(evt event.Event, fp, material string) worker.ProcessedEvent {
	snapshot := fingerprint.SnapshotFor(evt)
	if material == "" {
		material = snapshot.Material
	}
	evt.Fingerprint = fp
	evt.FingerprintMaterial = material
	evt.FingerprintExplanation = snapshot.Explanation
	return worker.ProcessedEvent{
		Event:                  evt,
		Fingerprint:            fp,
		FingerprintMaterial:    material,
		FingerprintExplanation: snapshot.Explanation,
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func issueFingerprints(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.db.Query(`SELECT fingerprint FROM issues ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var fps []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			t.Fatal(err)
		}
		fps = append(fps, fp)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return fps
}

// #188: two Alertmanager alerts with the same name and message but different
// labels arrive with distinct override fingerprints and identical material.
// The migration used to re-hash both to the material hash, rewriting the first
// and merging the second into it.
func TestMigrateFingerprintsKeepsOverrides(t *testing.T) {
	t.Parallel()
	store := openForMigration(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	for _, fp := range []string{"alertmanager:aaa", "alertmanager:bbb"} {
		if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(migrationEvent(at), fp, "")); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.migrateFingerprints(ctx); err != nil {
		t.Fatal(err)
	}

	got := issueFingerprints(t, store)
	if len(got) != 2 || got[0] != "alertmanager:aaa" || got[1] != "alertmanager:bbb" {
		t.Fatalf("override fingerprints changed by migration: %v", got)
	}
}

// An issue grouped by an older algorithm stores the hash of its older
// material. The migration must still bring it to the current fingerprint.
func TestMigrateFingerprintsRewritesStaleComputedFingerprint(t *testing.T) {
	t.Parallel()
	store := openForMigration(t)
	ctx := context.Background()
	evt := migrationEvent(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	const oldMaterial = `{"exceptionType":"alert","algorithm":"old"}`
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(evt, sha256Hex(oldMaterial), oldMaterial)); err != nil {
		t.Fatal(err)
	}

	if err := store.migrateFingerprints(ctx); err != nil {
		t.Fatal(err)
	}

	want := fingerprint.Fingerprint(evt)
	if got := issueFingerprints(t, store); len(got) != 1 || got[0] != want {
		t.Fatalf("fingerprints = %v, want [%s]", got, want)
	}
	var eventFP string
	if err := store.db.QueryRow(`SELECT fingerprint FROM events`).Scan(&eventFP); err != nil {
		t.Fatal(err)
	}
	if eventFP != want {
		t.Fatalf("event fingerprint = %s, want %s", eventFP, want)
	}
}

// When the recomputed fingerprint already belongs to another issue and merging
// is enabled, the stale issue is folded into it.
func TestMigrateFingerprintsMergesStaleIntoCurrent(t *testing.T) {
	t.Parallel()
	store := openForMigration(t)
	store.mergeFingerprintCollisions = true
	ctx := context.Background()
	first := migrationEvent(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	second := migrationEvent(time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC))

	current := fingerprint.Fingerprint(first)
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(first, current, "")); err != nil {
		t.Fatal(err)
	}
	const oldMaterial = `{"exceptionType":"alert","algorithm":"old"}`
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(second, sha256Hex(oldMaterial), oldMaterial)); err != nil {
		t.Fatal(err)
	}
	if got := issueFingerprints(t, store); len(got) != 2 {
		t.Fatalf("setup: want 2 issues, got %v", got)
	}

	if err := store.migrateFingerprints(ctx); err != nil {
		t.Fatal(err)
	}

	if got := issueFingerprints(t, store); len(got) != 1 || got[0] != current {
		t.Fatalf("fingerprints = %v, want [%s]", got, current)
	}
	var count int
	if err := store.db.QueryRow(`SELECT event_count FROM issues`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event_count = %d, want 2", count)
	}
}

// #241: a client that put the page URL in its message filed one issue per page.
// After the fingerprint stops seeing the path, the migration rewrites one of
// them in place and leaves the rest; a new event for another page then lands on
// the rewritten issue and is not new, so no new-issue mail goes out for it.
func TestMigrateFingerprintsURLRegroupDoesNotCreateIssues(t *testing.T) {
	t.Parallel()
	store := openForMigration(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	page := func(path string, observed time.Time) event.Event {
		evt := migrationEvent(observed)
		evt.Exception = event.Exception{Type: "*scrape.StatusError", Message: "GET https://mywed.com/" + path + ": 418 I'm a teapot"}
		return evt
	}
	const oldMaterial = `{"exceptionType":"*scrape.statuserror","algorithm":"per-url"}`
	for i, path := range []string{"en/france/lyon/", "en/italy/rome/", "nl/gent/"} {
		material := oldMaterial + path
		evt := page(path, at.Add(time.Duration(i)*time.Minute))
		if _, _, isNew, _, err := store.PersistProcessedEvent(ctx, withFingerprint(evt, sha256Hex(material), material)); err != nil || !isNew {
			t.Fatalf("setup %s: isNew=%v err=%v", path, isNew, err)
		}
	}

	if err := store.migrateFingerprints(ctx); err != nil {
		t.Fatal(err)
	}
	if got := issueFingerprints(t, store); len(got) != 3 {
		t.Fatalf("migration must not add or delete issues, got %d", len(got))
	}

	later := page("fr/paris/", at.Add(time.Hour))
	issue, _, isNew, _, err := store.PersistProcessedEvent(ctx, withFingerprint(later, fingerprint.Fingerprint(later), ""))
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Fatalf("an event for another page must group onto an existing issue, got new %s", issue.ID)
	}
	if got := issueFingerprints(t, store); len(got) != 3 {
		t.Fatalf("issues = %d, want 3", len(got))
	}
}

// By default a collision leaves the stale issue untouched: nothing is deleted,
// so its key keeps resolving and its history stays.
func TestMigrateFingerprintsLeavesCollidingIssueAloneByDefault(t *testing.T) {
	t.Parallel()
	store := openForMigration(t)
	ctx := context.Background()
	first := migrationEvent(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	second := migrationEvent(time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC))

	current := fingerprint.Fingerprint(first)
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(first, current, "")); err != nil {
		t.Fatal(err)
	}
	const oldMaterial = `{"exceptionType":"alert","algorithm":"old"}`
	oldFP := sha256Hex(oldMaterial)
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, withFingerprint(second, oldFP, oldMaterial)); err != nil {
		t.Fatal(err)
	}

	if err := store.migrateFingerprints(ctx); err != nil {
		t.Fatal(err)
	}

	got := issueFingerprints(t, store)
	if len(got) != 2 {
		t.Fatalf("both issues must survive, got %v", got)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("events = %d (err %v), want 2", count, err)
	}
	var stale int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE fingerprint = ?`, oldFP).Scan(&stale); err != nil || stale != 1 {
		t.Fatalf("the colliding issue must keep its old fingerprint, found %d (err %v)", stale, err)
	}
}
