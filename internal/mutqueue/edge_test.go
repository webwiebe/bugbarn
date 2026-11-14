package mutqueue_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

// A final line without a newline is a torn write. It is applied when it parses
// and dead-lettered when it does not, and the file is removed either way.
func TestDrainHandlesATornFinalLine(t *testing.T) {
	for name, tc := range map[string]struct {
		tail     string
		applied  string
		wantDead bool
	}{
		"valid":   {`{"op":"resolve","issue_id":"T"}`, "A,T", false},
		"corrupt": {`{"op":"res`, "A", true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := `{"op":"resolve","issue_id":"A"}` + "\n" + tc.tail
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
			if strings.Join(got, ",") != tc.applied {
				t.Errorf("applied = %v, want %s", got, tc.applied)
			}
			_, deadErr := os.Stat(filepath.Join(dir, "mutations.deadletter.ndjson"))
			if (deadErr == nil) != tc.wantDead {
				t.Errorf("dead letter present = %v, want %v", deadErr == nil, tc.wantDead)
			}
			if _, err := os.Stat(filepath.Join(dir, "mutations.processing.ndjson")); err == nil {
				t.Error("processing file must be removed")
			}
		})
	}
}

// An unreadable cursor restarts the file instead of failing the drain.
func TestDrainIgnoresAnUnreadableCursor(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "mutations.processing.ndjson")
	if err := os.WriteFile(proc, []byte(`{"op":"resolve","issue_id":"A"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proc+".cursor", []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	n := 0
	if err := q.Drain(func(mutqueue.Record) error { n++; return nil }); err != nil || n != 1 {
		t.Fatalf("drain = %v, applied %d, want 1", err, n)
	}
	if _, err := os.Stat(proc + ".cursor"); err == nil {
		t.Error("cursor must be removed with the file")
	}
}

// A cursor left behind by a crash after the file was removed is cleared.
func TestDrainClearsAStrayCursor(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "mutations.processing.ndjson.cursor")
	if err := os.WriteFile(stray, []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.Drain(func(mutqueue.Record) error { return nil }); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("stray cursor not cleared")
	}
}
