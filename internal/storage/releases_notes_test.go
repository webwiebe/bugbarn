package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A marker's notes are cut to maxReleaseNotesRunes on a rune boundary, on create
// and on update; short notes are stored as sent.
func TestReleaseNotesAreCappedOnARuneBoundary(t *testing.T) {
	t.Parallel()
	store, proj := openRevocationStore(t)
	ctx := domain.WithProjectID(context.Background(), proj.ID)

	long := strings.Repeat("é", maxReleaseNotesRunes+50)
	assertCapped := func(rel Release) {
		t.Helper()
		got, err := store.GetRelease(ctx, rel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if n := len([]rune(got.Notes)); n != maxReleaseNotesRunes {
			t.Fatalf("stored %d runes, want %d", n, maxReleaseNotesRunes)
		}
		if !strings.HasPrefix(long, got.Notes) {
			t.Fatal("the cut split a rune")
		}
	}

	rel, err := store.CreateRelease(ctx, Release{Name: "v1", Notes: long})
	if err != nil {
		t.Fatal(err)
	}
	assertCapped(rel)

	short := "deploy v2"
	rel, err = store.CreateRelease(ctx, Release{Name: "v2", Notes: short})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetRelease(ctx, rel.ID); err != nil || got.Notes != short {
		t.Fatalf("notes=%q err=%v, want them unchanged", got.Notes, err)
	}

	if _, err := store.UpdateRelease(ctx, rel.ID, Release{Name: "v2", Notes: long}); err != nil {
		t.Fatal(err)
	}
	assertCapped(rel)
}
