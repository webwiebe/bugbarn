package mutqueue_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

func TestNewFailsWhenTheDirectoryIsAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mutqueue.New(filepath.Join(blocker, "queue")); err == nil {
		t.Error("a queue cannot live under a regular file")
	}
}

// A crash between the claim and the delete leaves a processing file. The next
// drain applies it before it claims new records.
func TestDrainReplaysALeftoverProcessingFileFirst(t *testing.T) {
	dir := t.TempDir()
	leftover := `{"op":"resolve","issue_id":"OLD-1"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mutations.processing.ndjson"), []byte(leftover), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.Append(mutqueue.Record{Op: mutqueue.OpReopen, IssueID: "NEW-1"}); err != nil {
		t.Fatal(err)
	}

	var order []string
	if err := q.Drain(func(r mutqueue.Record) error {
		order = append(order, r.IssueID)
		return nil
	}); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if strings.Join(order, ",") != "OLD-1,NEW-1" {
		t.Errorf("order = %v, want the leftover first", order)
	}
}

func TestDrainReportsACorruptLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mutations.processing.ndjson"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	err = q.Drain(func(mutqueue.Record) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("drain = %v, want a parse error", err)
	}
}

func TestCloseIsIdempotentForANilFile(t *testing.T) {
	q, err := mutqueue.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
