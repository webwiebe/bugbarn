package ingestproc

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/spool"
)

func slugRecord(t *testing.T, id, slug string) spool.Record {
	t.Helper()
	return spool.Record{IngestID: id, ReceivedAt: time.Now().UTC(), BodyBase64: eventBody(t), ProjectSlug: slug}
}

// Both writers call PersistRecord, so the rule for an unknown project slug is
// one rule: active under auto-approve, pending and held otherwise.
func TestUnknownProjectFollowsAutoApprove(t *testing.T) {
	t.Parallel()

	t.Run("auto approve creates the project active and persists", func(t *testing.T) {
		t.Parallel()
		proc, store := newProcessor(t)
		res := proc.PersistRecord(context.Background(), slugRecord(t, "a", "fresh-active"))
		if res.Outcome != OutcomeSuccess {
			t.Fatalf("outcome = %v err=%v, want success", res.Outcome, res.Err)
		}
		proj, err := store.ProjectBySlug(context.Background(), "fresh-active")
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if proj.Status != "active" || res.ProjectID != proj.ID {
			t.Errorf("status=%q result project=%d, want active and %d", proj.Status, res.ProjectID, proj.ID)
		}
	})

	t.Run("without auto approve the project is pending and the payload held", func(t *testing.T) {
		t.Parallel()
		proc, store := pendingProcessor(t)
		res := proc.PersistRecord(context.Background(), slugRecord(t, "b", "fresh-pending"))
		if res.Outcome != OutcomeHeld {
			t.Fatalf("outcome = %v err=%v, want held", res.Outcome, res.Err)
		}
		proj, err := store.ProjectBySlug(context.Background(), "fresh-pending")
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if proj.Status != "pending" || res.ProjectID != proj.ID {
			t.Errorf("status=%q result project=%d, want pending and %d", proj.Status, res.ProjectID, proj.ID)
		}
	})
}

func TestPersistRecordReportsFingerprintAndSeverity(t *testing.T) {
	t.Parallel()
	proc, _ := newProcessor(t)
	res := proc.PersistRecord(context.Background(), slugRecord(t, "fp", ""))
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("outcome = %v err=%v", res.Outcome, res.Err)
	}
	if res.Fingerprint == "" || res.Severity == "" {
		t.Errorf("fingerprint=%q severity=%q, want both set", res.Fingerprint, res.Severity)
	}
	if res.IsNew != true || res.Issue.ID == "" {
		t.Errorf("first event should create an issue: %+v", res)
	}

	// A parse error carries no fingerprint, a held record does.
	bad := proc.PersistRecord(context.Background(), spool.Record{IngestID: "bad", BodyBase64: "!!!"})
	if bad.Outcome != OutcomeParseError || bad.Fingerprint != "" {
		t.Errorf("bad record: outcome=%v fingerprint=%q", bad.Outcome, bad.Fingerprint)
	}
	pend, _ := pendingProcessor(t)
	held := pend.PersistRecord(context.Background(), slugRecord(t, "h", "held-fp"))
	if held.Outcome != OutcomeHeld || held.Fingerprint == "" {
		t.Errorf("held record: outcome=%v fingerprint=%q", held.Outcome, held.Fingerprint)
	}
}

func TestPersistRecordPublishesDomainEvents(t *testing.T) {
	t.Parallel()
	proc, _ := newProcessor(t)
	bus := &domainevents.Bus{}
	var created, recorded int
	bus.Subscribe(func(e any) {
		switch e.(type) {
		case domainevents.IssueCreated:
			created++
		case domainevents.IssueEventRecorded:
			recorded++
		}
	})
	proc.publisher = domainevents.NewIssuePublisher(bus)

	for i := 0; i < 2; i++ {
		if res := proc.PersistRecord(context.Background(), slugRecord(t, "e", "events")); res.Outcome != OutcomeSuccess {
			t.Fatalf("outcome = %v err=%v", res.Outcome, res.Err)
		}
	}
	if created != 1 || recorded != 2 {
		t.Errorf("created=%d recorded=%d, want 1 and 2", created, recorded)
	}
}

// A lock failure and a deadline are retryable, any other persist failure is
// permanent. The deadline stands in for a lock that outlasts the storage cap.
func TestPersistRecordClassifiesFailures(t *testing.T) {
	t.Parallel()

	t.Run("expired deadline is transient", func(t *testing.T) {
		t.Parallel()
		proc, _ := newProcessor(t)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		res := proc.PersistRecord(ctx, slugRecord(t, "d", ""))
		if res.Outcome != OutcomeTransient || !errors.Is(res.Err, context.DeadlineExceeded) {
			t.Errorf("outcome=%v err=%v, want transient deadline", res.Outcome, res.Err)
		}
	})

	t.Run("closed store is a permanent persist error", func(t *testing.T) {
		t.Parallel()
		proc, store := newProcessor(t)
		store.Close()
		res := proc.PersistRecord(context.Background(), slugRecord(t, "c", ""))
		if res.Outcome != OutcomePersistError || res.Err == nil {
			t.Errorf("outcome=%v err=%v, want persist error", res.Outcome, res.Err)
		}
	})

	t.Run("a hold that hits the deadline is transient", func(t *testing.T) {
		t.Parallel()
		proc, _ := pendingProcessor(t)
		if _, ok := proc.EnsureProjectForIngest(context.Background(), "hold-slow"); !ok {
			t.Fatal("project not created")
		}
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		// Call holdEvent directly: under the expired context project resolution
		// would fail first and the record never reach the hold.
		proj, ok := proc.EnsureProjectForIngest(context.Background(), "hold-slow")
		if !ok {
			t.Fatal("project lookup failed")
		}
		res := proc.holdEvent(ctx, Result{}, proj, slugRecord(t, "hs", "hold-slow"))
		if res.Outcome != OutcomeTransient || res.ProjectID != proj.ID {
			t.Errorf("outcome=%v project=%d err=%v, want transient", res.Outcome, res.ProjectID, res.Err)
		}
	})
}

func spanNames(spans tracetest.SpanStubs) map[string]tracetest.SpanStub {
	out := make(map[string]tracetest.SpanStub, len(spans))
	for _, s := range spans {
		out[s.Name] = s
	}
	return out
}

func hasAttr(s tracetest.SpanStub, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestPersistRecordEmitsSpansOnlyWithATracer(t *testing.T) {
	t.Parallel()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	proc, _ := newProcessor(t)
	if res := proc.PersistRecord(context.Background(), slugRecord(t, "q", "quiet")); res.Outcome != OutcomeSuccess {
		t.Fatalf("outcome=%v err=%v", res.Outcome, res.Err)
	}
	for name := range spanNames(exp.GetSpans()) {
		if name == spanPersist || name == spanSymbolicate || name == spanResolveProject {
			t.Fatalf("span %q emitted without a tracer", name)
		}
	}

	proc.SetTracer(tp.Tracer("test"))
	res := proc.PersistRecord(context.Background(), slugRecord(t, "t", "traced"))
	if res.Outcome != OutcomeSuccess {
		t.Fatalf("outcome=%v err=%v", res.Outcome, res.Err)
	}
	spans := spanNames(exp.GetSpans())
	for _, name := range []string{spanResolveProject, spanSymbolicate, spanPersist} {
		if _, ok := spans[name]; !ok {
			t.Errorf("span %q missing; got %v", name, spans)
		}
	}
	if v, ok := hasAttr(spans[spanPersist], "issue_id"); !ok || v.AsString() != res.Issue.ID {
		t.Errorf("persist span issue_id = %v, want %q", v, res.Issue.ID)
	}
	if v, ok := hasAttr(spans[spanPersist], "is_new"); !ok || !v.AsBool() {
		t.Errorf("persist span is_new = %v, want true", v)
	}
	if v, ok := hasAttr(spans[spanResolveProject], "project_slug"); !ok || v.AsString() != "traced" {
		t.Errorf("resolve span project_slug = %v", v)
	}
	if _, ok := hasAttr(spans[spanResolveProject], "project_id"); !ok {
		t.Error("resolve span lacks project_id")
	}
}

func TestPersistRecordSpanMarksFailures(t *testing.T) {
	t.Parallel()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	proc, store := newProcessor(t)
	proc.SetTracer(tp.Tracer("test"))
	store.Close()
	res := proc.PersistRecord(context.Background(), slugRecord(t, "f", "doomed"))
	if res.Outcome != OutcomePersistError {
		t.Fatalf("outcome=%v", res.Outcome)
	}
	spans := spanNames(exp.GetSpans())
	if s := spans[spanResolveProject]; s.Status.Code == 0 {
		t.Errorf("resolve span status = %v, want error", s.Status)
	}
	if s := spans[spanPersist]; s.Status.Code == 0 {
		t.Errorf("persist span status = %v, want error", s.Status)
	}
}

func releaseRecord(body string, projectID int64) spool.Record {
	return spool.Record{
		IngestID:   "rel",
		Kind:       "release",
		ProjectID:  projectID,
		BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

func TestPersistReleaseCreatesTheMarker(t *testing.T) {
	t.Parallel()
	proc, store := newProcessor(t)
	ctx := context.Background()
	proj, err := store.EnsureProject(ctx, "rel-project")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	body := `{"name":"v1.2.3","environment":"production","observedAt":"2026-01-02T03:04:05Z",` +
		`"version":"1.2.3","commitSha":"abc","url":"https://x","notes":"n","createdBy":"ci"}`
	if err := proc.PersistRelease(ctx, releaseRecord(body, proj.ID)); err != nil {
		t.Fatalf("PersistRelease: %v", err)
	}
	releases, err := store.ListReleases(domain.WithProjectID(ctx, proj.ID))
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(releases))
	}
	r := releases[0]
	if r.Name != "v1.2.3" || r.Environment != "production" || r.CommitSHA != "abc" || r.CreatedBy != "ci" {
		t.Errorf("release = %+v", r)
	}
	if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !r.ObservedAt.Equal(want) {
		t.Errorf("observedAt = %v, want %v", r.ObservedAt, want)
	}
}

func TestPersistReleaseRejectsBadBodies(t *testing.T) {
	t.Parallel()
	proc, _ := newProcessor(t)
	ctx := context.Background()

	if err := proc.PersistRelease(ctx, spool.Record{BodyBase64: "!!!"}); err == nil {
		t.Error("undecodable base64 should fail")
	}
	if err := proc.PersistRelease(ctx, releaseRecord("{not json", 0)); err == nil {
		t.Error("invalid JSON should fail")
	}
}
