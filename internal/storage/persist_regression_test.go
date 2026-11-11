package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// A persisted event reports its issue as new once and writes a regression row
// only through recordRegression, which stores the time in the sortable form.
func TestRecordRegressionWritesOneRow(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()

	at := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	issue, _, isNew, regressed, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
		ReceivedAt: at, ObservedAt: at, Severity: "ERROR", Message: "boom",
		Exception: event.Exception{Type: "panic", Message: "boom"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !isNew || regressed {
		t.Fatalf("first event: isNew=%v regressed=%v, want true/false", isNew, regressed)
	}
	if issue.ProjectSlug == "" {
		t.Fatal("issue carries no project slug")
	}

	var issueRowID int64
	if err := store.db.QueryRow(`SELECT id FROM issues LIMIT 1`).Scan(&issueRowID); err != nil {
		t.Fatal(err)
	}
	store.recordRegression(ctx, store.defaultProjectID, issueRowID, at)

	var n int
	var stored string
	if err := store.db.QueryRow(`SELECT COUNT(*), MAX(regressed_at) FROM regression_events`).Scan(&n, &stored); err != nil {
		t.Fatal(err)
	}
	if n != 1 || stored != formatTime(at) {
		t.Fatalf("regression_events: n=%d regressed_at=%q, want 1 row at %q", n, stored, formatTime(at))
	}
}
