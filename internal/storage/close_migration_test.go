package storage

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
)

type recordedLevels struct {
	mu     sync.Mutex
	levels []slog.Level
}

func (r *recordedLevels) Enabled(context.Context, slog.Level) bool { return true }
func (r *recordedLevels) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.levels = append(r.levels, rec.Level)
	return nil
}
func (r *recordedLevels) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recordedLevels) WithGroup(string) slog.Handler      { return r }

// Closing a store right after opening it stops the background fingerprint
// migration and must not report that as an error: selflog files every ERROR
// record as a bug against BugBarn itself.
func TestCloseRightAfterOpenLogsNoError(t *testing.T) {
	rec := &recordedLevels{}
	prev := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		store, err := Open(filepath.Join(dir, "bugbarn.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, l := range rec.levels {
		if l >= slog.LevelError {
			t.Fatalf("a record at %v was logged while closing a fresh store", l)
		}
	}
}
