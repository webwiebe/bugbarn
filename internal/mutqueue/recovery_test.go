package mutqueue_test

import (
	"bytes"
	"errors"
	"log/slog"
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

// A failure in the middle of a batch must not re-apply the records before it.
func TestDrainResumesAfterTheLastAppliedRecord(t *testing.T) {
	dir := t.TempDir()
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, id := range []string{"A", "B", "C"} {
		if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: id}); err != nil {
			t.Fatal(err)
		}
	}

	var first []string
	err = q.Drain(func(r mutqueue.Record) error {
		if r.IssueID == "B" {
			return errors.New("database busy")
		}
		first = append(first, r.IssueID)
		return nil
	})
	if err == nil {
		t.Fatal("drain must report the failed apply")
	}

	var second []string
	if err := q.Drain(func(r mutqueue.Record) error {
		second = append(second, r.IssueID)
		return nil
	}); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if strings.Join(first, ",") != "A" || strings.Join(second, ",") != "B,C" {
		t.Errorf("first = %v, second = %v; want A then B,C with no repeat", first, second)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "mutations.processing*")); len(left) != 0 {
		t.Errorf("leftover files after a full drain: %v", left)
	}
}

// A line that does not parse goes to the dead letter with an ERROR log and the
// records around it still apply.
func TestDrainDeadLettersACorruptLine(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	body := `{"op":"resolve","issue_id":"A"}` + "\n{not json\n" + `{"op":"reopen","issue_id":"C"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mutations.processing.ndjson"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var got []string
	if err := q.Drain(func(r mutqueue.Record) error {
		got = append(got, r.IssueID)
		return nil
	}); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if strings.Join(got, ",") != "A,C" {
		t.Errorf("applied = %v, want A,C", got)
	}
	dead, err := os.ReadFile(filepath.Join(dir, "mutations.deadletter.ndjson"))
	if err != nil || strings.TrimSpace(string(dead)) != "{not json" {
		t.Errorf("dead letter = %q, %v", dead, err)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "dead-lettering") {
		t.Errorf("no ERROR log for the dead-lettered line: %s", logs.String())
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
