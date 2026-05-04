package logs

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	entries []domain.LogEntry
	err     error
}

func (f *fakeRepo) InsertLogEntries(_ context.Context, entries []domain.LogEntry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, entries...)
	return nil
}

func (f *fakeRepo) ListLogEntries(_ context.Context, _ int64, _ int, _ string, _ int, _ int64) ([]domain.LogEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func TestInsert_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := New(repo, nil)

	entries := []domain.LogEntry{
		{Message: "hello", Level: "info", LevelNum: 4},
		{Message: "world", Level: "warn", LevelNum: 8},
	}

	err := svc.Insert(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.entries) != 2 {
		t.Errorf("entries stored: got %d, want 2", len(repo.entries))
	}
}

func TestInsert_Error(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{err: apperr.Internal("db write failed", nil)}
	svc := New(repo, nil)

	err := svc.Insert(context.Background(), []domain.LogEntry{{Message: "fail"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

func TestList_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{entries: []domain.LogEntry{
		{ID: 1, Message: "log line 1"},
		{ID: 2, Message: "log line 2"},
	}}
	svc := New(repo, nil)

	got, err := svc.List(context.Background(), 1, 0, "", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("entries: got %d, want 2", len(got))
	}
}
