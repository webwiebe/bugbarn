package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func pendingOf(records []spool.Record) []spool.Pending {
	out := make([]spool.Pending, len(records))
	for i, r := range records {
		out[i] = spool.Pending{Record: r, After: spool.Position{Offset: int64(i + 1)}}
	}
	return out
}

// runOnceSummary runs worker-once and returns the JSON it printed.
func runOnceSummary(t *testing.T, dir string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	runErr := runWorkerOnce(testConfig(t, dir))
	os.Stdout = stdout
	w.Close()
	if runErr != nil {
		t.Fatalf("runWorkerOnce: %v", runErr)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatalf("read output: %v", err)
	}
	return out.String()
}

// worker-once starts at the worker's cursor, so records the worker already
// handled are not stored again, and a second run finds nothing.
func TestWorkerOnceHonorsTheCursorAndAdvancesIt(t *testing.T) {
	dir := t.TempDir()
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	appendRecords(t, sp, goodRecord(t, "c-1"))
	handled := activeSize(t, dir)
	appendRecords(t, sp, goodRecord(t, "c-2"), goodRecord(t, "c-3"))
	sp.Close()
	if err := spool.WriteCursor(dir, handled); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	if out := runOnceSummary(t, dir); !strings.Contains(out, `"records":2`) {
		t.Errorf("first run = %s, want the 2 records after the cursor", out)
	}
	if got := mustPosition(t, dir).Offset; got != activeSize(t, dir) {
		t.Errorf("cursor = %d, want the end of the spool (%d)", got, activeSize(t, dir))
	}
	if out := runOnceSummary(t, dir); !strings.Contains(out, `"records":0`) {
		t.Errorf("second run = %s, want nothing pending", out)
	}
}

// With a rotated segment the cursor still names, worker-once drains it, moves
// to the active segment and deletes the rotated one.
func TestWorkerOnceDrainsARotatedSegmentThenTheActiveOne(t *testing.T) {
	dir := t.TempDir()
	sp, err := spool.New(dir)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer sp.Close()
	appendRecords(t, sp, goodRecord(t, "r-1"), goodRecord(t, "r-2"))
	segment, err := sp.RotateIfExceedsAt(1, 0)
	if err != nil || segment == "" {
		t.Fatalf("rotate = %q, %v", segment, err)
	}
	appendRecords(t, sp, goodRecord(t, "r-3"))

	if out := runOnceSummary(t, dir); !strings.Contains(out, `"records":3`) {
		t.Errorf("run = %s, want 3 records across both segments", out)
	}
	if exists, _ := spool.SegmentExists(dir, segment); exists {
		t.Error("the drained rotated segment must be deleted")
	}
	if pos := mustPosition(t, dir); pos.Segment != "" || pos.Offset != activeSize(t, dir) {
		t.Errorf("cursor = %+v, want the end of the active segment", pos)
	}
}
