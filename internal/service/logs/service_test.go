package logs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	entries []domain.LogEntry
	err     error
	// failures is how many leading InsertLogEntries calls return err before the
	// repo starts accepting batches; 0 means err (when set) is permanent.
	failures int
	calls    int
}

func (f *fakeRepo) InsertLogEntries(_ context.Context, entries []domain.LogEntry) error {
	f.calls++
	if f.err != nil && (f.failures == 0 || f.calls <= f.failures) {
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

func TestInsert_CanceledNotLoggedAsError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := New(&fakeRepo{}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Insert(ctx, []domain.LogEntry{{Message: "x"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// A client disconnect must not surface as an ERROR (selflog captures those).
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("canceled insert logged at ERROR: %s", buf.String())
	}
}

// A batch that outlasts the storage layer's own per-insert ceiling comes back as
// context.DeadlineExceeded, which is write-lock contention wearing a different
// error. It must be retried, exactly like a raw SQLITE_BUSY.
func TestInsert_RetriesWriterDeadline(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repo := &fakeRepo{err: context.DeadlineExceeded, failures: 2}
	svc := New(repo, logger)

	if err := svc.Insert(context.Background(), []domain.LogEntry{{Message: "x"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.calls != 3 {
		t.Errorf("insert attempts: got %d, want 3", repo.calls)
	}
	if len(repo.entries) != 1 {
		t.Errorf("entries stored: got %d, want 1", len(repo.entries))
	}
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("recovered insert logged at ERROR: %s", buf.String())
	}
}

// Once the retries are spent the batch still belongs to the caller, which
// retries it on its own backoff. Nothing is lost here, so this must not log at
// ERROR — selflog captures those and files them as issues against us (BS2-83).
func TestInsert_ContendedNotLoggedAsError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repo := &fakeRepo{err: context.DeadlineExceeded}
	svc := New(repo, logger)

	err := svc.Insert(context.Background(), []domain.LogEntry{{Message: "x"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if repo.calls != insertMaxAttempts {
		t.Errorf("insert attempts: got %d, want %d", repo.calls, insertMaxAttempts)
	}
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("contended insert logged at ERROR: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("contended insert not logged at WARN: %s", buf.String())
	}
}

// A permanent failure is neither retried nor softened: it loses the batch, so it
// stays an ERROR.
func TestInsert_PermanentErrorNotRetried(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repo := &fakeRepo{err: apperr.Internal("db write failed", nil)}
	svc := New(repo, logger)

	if err := svc.Insert(context.Background(), []domain.LogEntry{{Message: "x"}}); err == nil {
		t.Fatal("expected error, got nil")
	}
	if repo.calls != 1 {
		t.Errorf("insert attempts: got %d, want 1", repo.calls)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("permanent insert failure not logged at ERROR: %s", buf.String())
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
