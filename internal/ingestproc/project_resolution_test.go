package ingestproc

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

// A record that names a project but cannot be resolved is a failure. It is
// never stored under the default project.
func TestPersistRecordFailsWhenTheNamedProjectCannotBeResolved(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	store.Close()

	res := proc.PersistRecord(context.Background(), spool.Record{
		IngestID: "r1", ReceivedAt: time.Now().UTC(), BodyBase64: eventBody(t), ProjectSlug: "named",
	})
	if res.Outcome != OutcomePersistError || res.Err == nil {
		t.Fatalf("outcome = %v err = %v, want OutcomePersistError", res.Outcome, res.Err)
	}
}

func TestPersistRecordWithAnExpiredDeadlineIsTransientAtProjectResolution(t *testing.T) {
	t.Parallel()
	proc, _ := newProcessor(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	res := proc.PersistRecord(ctx, spool.Record{
		IngestID: "r2", ReceivedAt: time.Now().UTC(), BodyBase64: eventBody(t), ProjectSlug: "named",
	})
	if res.Outcome != OutcomeTransient {
		t.Fatalf("outcome = %v err = %v, want OutcomeTransient", res.Outcome, res.Err)
	}
}

// A record without a slug belongs to the default project.
func TestPersistRecordWithoutASlugUsesTheDefaultProject(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	ctx := context.Background()

	res := proc.PersistRecord(ctx, spool.Record{IngestID: "r3", ReceivedAt: time.Now().UTC(), BodyBase64: eventBody(t)})
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("outcome = %v err = %v", res.Outcome, res.Err)
	}
	issues, err := store.ListIssues(ctx)
	if err != nil || len(issues) != 1 {
		t.Fatalf("default project issues = %d, %v, want 1", len(issues), err)
	}
}
