package mutqueue_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

func quietLogs(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func openQueue(t *testing.T, dir string) *mutqueue.Queue {
	t.Helper()
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return q
}

func noApply(t *testing.T) func(mutqueue.Record) error {
	return func(r mutqueue.Record) error {
		t.Errorf("apply must not run, got %+v", r)
		return nil
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	q, err := mutqueue.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "A"}); err == nil {
		t.Fatal("Append on a closed queue must fail")
	}
}

func TestDrainFailsWhenTheProcessingPathCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	// A directory where the processing file belongs opens but cannot be read.
	if err := os.Mkdir(filepath.Join(dir, "mutations.processing.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}
	q := openQueue(t, dir)
	if err := q.Drain(noApply(t)); err == nil {
		t.Fatal("Drain must report a processing path it cannot read")
	}
}

func TestDrainFailsWhenTheClaimRenameIsRefused(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir)
	if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "A"}); err != nil {
		t.Fatal(err)
	}
	// A read-only directory refuses the rename of the active file.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := q.Drain(noApply(t))
	if err == nil || !strings.Contains(err.Error(), "claim") {
		t.Fatalf("Drain = %v, want a claim error", err)
	}
}

func TestDrainFailsWhenTheDeadLetterFileCannotBeOpened(t *testing.T) {
	quietLogs(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "mutations.deadletter.ndjson"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mutations.processing.ndjson"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := openQueue(t, dir)
	err := q.Drain(noApply(t))
	if err == nil || !strings.Contains(err.Error(), "dead letter") {
		t.Fatalf("Drain = %v, want a dead letter error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "mutations.processing.ndjson")); statErr != nil {
		t.Fatalf("the unparsed line must stay queued for the next drain: %v", statErr)
	}
}

func TestDrainFailsWhenTheCursorCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "mutations.processing.ndjson")
	if err := os.WriteFile(proc, []byte(`{"op":"resolve","issue_id":"A"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory where the cursor file belongs cannot be read as a cursor.
	if err := os.Mkdir(proc+".cursor", 0o755); err != nil {
		t.Fatal(err)
	}
	q := openQueue(t, dir)
	err := q.Drain(noApply(t))
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("Drain = %v, want a cursor error", err)
	}
}

func TestDrainFailsWhenTheCursorCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "mutations.processing.ndjson")
	if err := os.WriteFile(proc, []byte(`{"op":"resolve","issue_id":"A"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory at the cursor's temp path makes the cursor write fail.
	if err := os.Mkdir(proc+".cursor.tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	q := openQueue(t, dir)
	applied := 0
	err := q.Drain(func(mutqueue.Record) error { applied++; return nil })
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("Drain = %v, want a cursor write error", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
}

func TestApplyErrorNamesTheOperationAndIssue(t *testing.T) {
	dir := t.TempDir()
	q := openQueue(t, dir)
	if err := q.Append(mutqueue.Record{Op: mutqueue.OpMute, IssueID: "PROJ-9", MuteMode: "forever"}); err != nil {
		t.Fatal(err)
	}
	err := q.Drain(func(mutqueue.Record) error { return io.ErrUnexpectedEOF })
	if err == nil || !strings.Contains(err.Error(), "mute PROJ-9") {
		t.Fatalf("Drain = %v, want the op and issue in the error", err)
	}
}
