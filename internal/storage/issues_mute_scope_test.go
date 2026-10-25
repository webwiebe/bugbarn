package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// A mute addressed by display ID must land whatever project the request
// context carries. An API key without X-BugBarn-Project falls back to the
// default project, and the UPDATE used to match zero rows there while
// MuteIssue returned the unchanged issue with no error (bugbarn#232).
func TestMuteUnmuteIgnoreRequestProjectScope(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	issue, _, _, _, err := store.PersistProcessedEvent(ctx, processedEventFrom(event.Event{
		ObservedAt: time.Now().UTC(),
		ReceivedAt: time.Now().UTC(),
		Severity:   "ERROR",
		Message:    "scope test",
		Exception:  event.Exception{Type: "ScopeError", Message: "scope test"},
	}))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	other, err := store.CreateProject(ctx, "Other", "other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	otherCtx := domain.WithProjectID(ctx, other.ID)

	muted, err := store.MuteIssue(otherCtx, issue.ID, "until_regression")
	if err != nil {
		t.Fatalf("MuteIssue: %v", err)
	}
	if muted.Status != "muted" {
		t.Fatalf("status after mute = %q, want muted", muted.Status)
	}
	readBack, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.Status != "muted" || readBack.MuteMode != "until_regression" {
		t.Fatalf("read back status=%q mode=%q", readBack.Status, readBack.MuteMode)
	}

	unmuted, err := store.UnmuteIssue(otherCtx, issue.ID)
	if err != nil {
		t.Fatalf("UnmuteIssue: %v", err)
	}
	if unmuted.Status != "unresolved" || unmuted.MuteMode != "" {
		t.Fatalf("after unmute status=%q mode=%q", unmuted.Status, unmuted.MuteMode)
	}
}
