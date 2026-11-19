package projects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestArchiveReturnsTheLastEventTime(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{projects: map[string]domain.Project{}, archivedLast: last}
	got, err := New(repo, nil).Archive(context.Background(), "legacy")
	if err != nil || !got.Equal(last) || repo.archived != "legacy" {
		t.Fatalf("got %v, %v, archived %q", got, err, repo.archived)
	}
	if err := New(repo, nil).Unarchive(context.Background(), "legacy"); err != nil || repo.unarchived != "legacy" {
		t.Fatalf("unarchive: %v, %q", err, repo.unarchived)
	}
}

func TestArchiveAndUnarchivePassStoreErrorsThrough(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	repo := &fakeRepo{projects: map[string]domain.Project{}, err: boom}
	svc := New(repo, nil)
	if _, err := svc.Archive(context.Background(), "x"); !errors.Is(err, boom) {
		t.Errorf("archive err = %v", err)
	}
	if err := svc.Unarchive(context.Background(), "x"); !errors.Is(err, boom) {
		t.Errorf("unarchive err = %v", err)
	}
}

func TestFormatLast(t *testing.T) {
	t.Parallel()
	if formatLast(time.Time{}) != "never" {
		t.Error("a zero time must read never")
	}
	if got := formatLast(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "2026-01-02T03:04:05Z" {
		t.Errorf("got %q", got)
	}
}
