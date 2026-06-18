package mutqueue_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/mutqueue"
)

func TestAppendAndDrain(t *testing.T) {
	dir := t.TempDir()

	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	records := []mutqueue.Record{
		{Op: mutqueue.OpResolve, IssueID: "PROJ-1"},
		{Op: mutqueue.OpReopen, IssueID: "PROJ-2"},
		{Op: mutqueue.OpMute, IssueID: "PROJ-3", MuteMode: "forever"},
		{Op: mutqueue.OpUnmute, IssueID: "PROJ-4"},
	}
	for _, r := range records {
		if err := q.Append(r); err != nil {
			t.Fatalf("append %s: %v", r.IssueID, err)
		}
	}

	var got []mutqueue.Record
	if err := q.Drain(func(r mutqueue.Record) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != len(records) {
		t.Fatalf("got %d records, want %d", len(got), len(records))
	}
	for i, want := range records {
		if got[i].Op != want.Op || got[i].IssueID != want.IssueID || got[i].MuteMode != want.MuteMode {
			t.Errorf("record %d: got %+v, want %+v", i, got[i], want)
		}
	}
}

func TestDrainEmpty(t *testing.T) {
	dir := t.TempDir()

	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if err := q.Drain(func(mutqueue.Record) error { return nil }); err != nil {
		t.Fatalf("drain on empty queue: %v", err)
	}
}

func TestDrainRetainsFileOnApplyError(t *testing.T) {
	dir := t.TempDir()

	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "PROJ-1"}); err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("db locked")
	if err := q.Drain(func(r mutqueue.Record) error {
		return applyErr
	}); err == nil {
		t.Fatal("expected error from Drain, got nil")
	}

	// The processing file should still be present so the next Drain retries.
	procPath := filepath.Join(dir, "mutations.processing.ndjson")
	if _, err := filepath.Glob(procPath); err != nil {
		t.Fatalf("processing file stat: %v", err)
	}

	var retried []mutqueue.Record
	if err := q.Drain(func(r mutqueue.Record) error {
		retried = append(retried, r)
		return nil
	}); err != nil {
		t.Fatalf("retry drain: %v", err)
	}
	if len(retried) != 1 || retried[0].IssueID != "PROJ-1" {
		t.Fatalf("retry got %+v, want [{PROJ-1}]", retried)
	}
}

func TestAppendAfterDrain(t *testing.T) {
	dir := t.TempDir()

	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Drain(func(mutqueue.Record) error { return nil }); err != nil {
		t.Fatal(err)
	}

	// Append to the fresh file opened after drain.
	if err := q.Append(mutqueue.Record{Op: mutqueue.OpReopen, IssueID: "B"}); err != nil {
		t.Fatal(err)
	}

	var got []mutqueue.Record
	if err := q.Drain(func(r mutqueue.Record) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IssueID != "B" {
		t.Fatalf("got %+v, want [{IssueID:B}]", got)
	}
}

func TestQueuedAtIsSet(t *testing.T) {
	dir := t.TempDir()

	q, err := mutqueue.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if err := q.Append(mutqueue.Record{Op: mutqueue.OpResolve, IssueID: "X"}); err != nil {
		t.Fatal(err)
	}

	var got mutqueue.Record
	if err := q.Drain(func(r mutqueue.Record) error {
		got = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if got.QueuedAt.IsZero() {
		t.Error("QueuedAt was not set")
	}
}
