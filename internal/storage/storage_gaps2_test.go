package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestGroupsLifecycleAndErrorKinds(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()

	g, err := s.CreateGroup(ctx, "Backend", "backend")
	if err != nil || g.ID == 0 {
		t.Fatalf("CreateGroup = %+v, %v", g, err)
	}
	wantKind(t, errOf(s.CreateGroup(ctx, "Again", "backend")), apperr.ErrConflict)

	if _, err := s.CreateProject(ctx, "Svc", "svc"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.AssignProjectToGroup(ctx, "svc", "ghost"), apperr.ErrNotFound)
	wantKind(t, s.AssignProjectToGroup(ctx, "ghost", "backend"), apperr.ErrNotFound)
	if err := s.AssignProjectToGroup(ctx, "svc", "backend"); err != nil {
		t.Fatal(err)
	}
	members, err := s.ListGroupProjects(ctx, "backend")
	if err != nil || len(members) != 1 || members[0].Slug != "svc" {
		t.Fatalf("ListGroupProjects = %+v, %v", members, err)
	}
	wantKind(t, errOf(s.ListGroupProjects(ctx, "ghost")), apperr.ErrNotFound)

	groups, err := s.ListGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("ListGroups = %+v, %v", groups, err)
	}
	if err := s.RemoveProjectFromGroup(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.RemoveProjectFromGroup(ctx, "ghost"), apperr.ErrNotFound)
	if err := s.DeleteGroup(ctx, "backend"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.DeleteGroup(ctx, "backend"), apperr.ErrNotFound)
}

func TestGroupReadsOnClosedStoreAreInternal(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	s.Close()
	wantKind(t, errOf(s.ListGroups(ctx)), apperr.ErrInternal)
	wantKind(t, errOf(s.ListProjects(ctx)), apperr.ErrInternal)
	wantKind(t, errOf(s.ProjectUsageAll(ctx)), apperr.ErrInternal)
	// A database failure is no longer reported as "group not found".
	wantKind(t, s.AssignProjectToGroup(ctx, "a", "b"), apperr.ErrInternal)
	wantKind(t, errOf(s.ListGroupProjects(ctx, "b")), apperr.ErrInternal)
}

func TestRenameProjectCreatesAliasAndRejectsTakenSlug(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, "Old", "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, "Taken", "taken"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.RenameProject(ctx, "ghost", "x", "X"), apperr.ErrNotFound)
	wantKind(t, s.RenameProject(ctx, "old", "taken", "Old"), apperr.ErrConflict)
	if err := s.RenameProject(ctx, "old", "fresh", "Fresh"); err != nil {
		t.Fatal(err)
	}
	p, err := s.ProjectBySlug(ctx, "fresh")
	if err != nil || p.Name != "Fresh" {
		t.Fatalf("renamed project = %+v, %v", p, err)
	}
	id, err := s.ResolveAlias(ctx, "old")
	if err != nil || id != p.ID {
		t.Fatalf("alias = %d, %v", id, err)
	}
}

func TestMergeProjectsMovesRowsAndReportsMissingSides(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	src, err := s.CreateProject(ctx, "Src", "src")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := s.CreateProject(ctx, "Dst", "dst")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(ctx, "k", src.ID, "merge-hash", APIKeyScopeIngest); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.MergeProjects(ctx, "ghost", "dst"), apperr.ErrNotFound)
	wantKind(t, s.MergeProjects(ctx, "src", "ghost"), apperr.ErrNotFound)
	if err := s.MergeProjects(ctx, "src", "dst"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, errOf(s.ProjectBySlug(ctx, "src")), apperr.ErrNotFound)
	if id, err := s.ResolveAlias(ctx, "src"); err != nil || id != dst.ID {
		t.Fatalf("alias after merge = %d, %v", id, err)
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys of target = %+v, %v", keys, err)
	}
}

func TestApplyTimingsModes(t *testing.T) {
	t.Parallel()
	hour, grace := time.Hour, 5*time.Minute
	cron, tz := "0 2 * * *", "Europe/Amsterdam"

	interval := MonitorCheckIn{Interval: &hour, Grace: &grace}
	m := domain.Monitor{Schedule: "old", Timezone: "UTC"}
	if err := applyTimings(&m, interval); err != nil || m.Interval != hour || m.Schedule != "" || m.Timezone != "" || m.Grace != grace {
		t.Fatalf("interval mode = %+v, %v", m, err)
	}
	m = domain.Monitor{Timezone: "UTC"}
	if err := applyTimings(&m, MonitorCheckIn{Schedule: &cron}); err != nil || m.Schedule != cron || m.Timezone != "UTC" || m.Interval != 0 {
		t.Fatalf("schedule keeps the old zone = %+v, %v", m, err)
	}
	if err := applyTimings(&m, MonitorCheckIn{Schedule: &cron, Timezone: &tz}); err != nil || m.Timezone != tz {
		t.Fatalf("schedule with zone = %+v, %v", m, err)
	}
	if err := applyTimings(&m, MonitorCheckIn{Timezone: &tz}); err != nil {
		t.Fatalf("zone-only change on a cron monitor: %v", err)
	}
	intervalMonitor := domain.Monitor{Interval: hour}
	wantKind(t, applyTimings(&intervalMonitor, MonitorCheckIn{Timezone: &tz}), apperr.ErrInvalidInput)
	if got := valueOr(nil, "fallback"); got != "fallback" {
		t.Fatalf("valueOr(nil) = %q", got)
	}
}

func TestMonitorCheckInWithoutTimingsOnUnknownMonitorIsNotFound(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	_, err := s.RecordMonitorCheckIn(context.Background(), MonitorCheckIn{ProjectID: s.DefaultProjectID(), Slug: "nope", Status: "ok", At: time.Now()})
	wantKind(t, err, apperr.ErrNotFound)
}

func TestStringSliceAndTitleHelpers(t *testing.T) {
	t.Parallel()
	var got []string
	if err := unmarshalStringSlice(nil, &got); err != nil || got != nil {
		t.Fatalf("empty = %v, %v", got, err)
	}
	if err := unmarshalStringSlice([]byte(`["a","b"]`), &got); err != nil || len(got) != 2 {
		t.Fatalf("valid = %v, %v", got, err)
	}
	if err := unmarshalStringSlice([]byte(`{bad`), &got); err == nil || got != nil {
		t.Fatalf("invalid = %v, %v", got, err)
	}
	if mustMarshalStrings(nil) != "[]" || mustMarshalStrings([]string{"x"}) != `["x"]` {
		t.Fatal("mustMarshalStrings")
	}

	titles := map[string]event.Event{
		"typeerror: boom": {Exception: event.Exception{Type: "TypeError", Message: " boom "}},
		"typeerror":       {Exception: event.Exception{Type: "TypeError"}},
		"plain message":   {Message: "Plain Message"},
		"from message":    {Exception: event.Exception{Type: " ", Message: ""}, Message: "From Message"},
	}
	for want, evt := range titles {
		if got := fingerprintFromEvent(evt); got != want {
			t.Errorf("fingerprintFromEvent(%+v) = %q, want %q", evt, got, want)
		}
	}

	if raw := rawScrubbedFallback(nil); raw != (rawScrubbedData{}) {
		t.Fatalf("nil payload = %+v", raw)
	}
	if raw := rawScrubbedFallback(map[string]any{"name": "N"}); raw.name != "N" || raw.message != "" {
		t.Fatalf("no properties = %+v", raw)
	}
	raw := rawScrubbedFallback(map[string]any{"name": "N", "properties": map[string]any{"message": "M", "url": "U"}})
	if raw.name != "N" || raw.message != "M" || raw.source != "U" {
		t.Fatalf("url fallback = %+v", raw)
	}
	raw = rawScrubbedFallback(map[string]any{"properties": map[string]any{"source": "S", "url": "U"}})
	if raw.source != "S" {
		t.Fatalf("source wins over url: %+v", raw)
	}
}

func TestDisplayIssueIDAndIssueSeenAt(t *testing.T) {
	t.Parallel()
	if got := displayIssueID("BW", 7, 99); got != "BW-7" {
		t.Fatalf("with prefix = %q", got)
	}
	if got := displayIssueID("", 7, 99); got != "issue-99" {
		t.Fatalf("legacy = %q", got)
	}
	if got := displayIssueID("BW", 0, 99); got != "issue-99" {
		t.Fatalf("zero number = %q", got)
	}

	observed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	received := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	if got := issueSeenAt(event.Event{ObservedAt: observed, ReceivedAt: received}); !got.Equal(observed) || got.Location() != time.UTC {
		t.Fatalf("observed wins and is UTC: %v", got)
	}
	if got := issueSeenAt(event.Event{ReceivedAt: received}); !got.Equal(received) {
		t.Fatalf("received fallback: %v", got)
	}
	if got := issueSeenAt(event.Event{}); time.Since(got) > time.Minute || got.Location() != time.UTC {
		t.Fatalf("now fallback: %v", got)
	}
}

func TestNullIntToPtr(t *testing.T) {
	t.Parallel()
	if nullIntToPtr(sql.NullInt64{}) != nil {
		t.Fatal("null is nil")
	}
	if p := nullIntToPtr(sql.NullInt64{Int64: 9, Valid: true}); p == nil || *p != 9 {
		t.Fatalf("valid = %v", p)
	}
}

func TestPruneReleasesGuards(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	for _, tc := range [][2]int{{-1, 10}, {3, 0}, {3, -5}} {
		if n, err := s.PruneReleases(ctx, tc[0], tc[1]); err != nil || n != 0 {
			t.Fatalf("PruneReleases(%d, %d) = %d, %v", tc[0], tc[1], n, err)
		}
	}
}
