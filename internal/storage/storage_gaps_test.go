package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openReadOnlyTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bugbarn.db")
	rw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rw.Close()
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })
	return ro
}

func wantKind(t *testing.T, err, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("error = %v, want kind %v", err, kind)
	}
}

func errOf[T any](_ T, err error) error { return err }

func TestDeleteAPIKeyReturnsNotFoundForUnknownID(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	wantKind(t, s.DeleteAPIKey(ctx, 424242), apperr.ErrNotFound)

	k, err := s.CreateAPIKey(ctx, "k", s.DefaultProjectID(), "hash-del", APIKeyScopeIngest)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, k.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	wantKind(t, errOf(s.APIKeyByID(ctx, k.ID)), apperr.ErrNotFound)
}

func TestAPIKeyByIDReturnsRow(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	k, err := s.CreateAPIKey(ctx, "named", s.DefaultProjectID(), "hash-byid", APIKeyScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.APIKeyByID(ctx, k.ID)
	if err != nil || got.Name != "named" || got.Scope != APIKeyScopeRead {
		t.Fatalf("APIKeyByID = %+v, %v", got, err)
	}
}

func TestSettingsRoundTripPerProject(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := domain.WithProjectID(context.Background(), s.DefaultProjectID())
	if err := s.UpdateSettings(ctx, map[string]string{"theme": "dark", "  ": "ignored", "lang": "nl"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings(ctx, map[string]string{"theme": "light"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["theme"] != "light" || got["lang"] != "nl" {
		t.Fatalf("settings = %v", got)
	}
	other, err := s.CreateProject(ctx, "Other", "other")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.GetSettings(domain.WithProjectID(ctx, other.ID))
	if err != nil || len(empty) != 0 {
		t.Fatalf("other project settings = %v, %v", empty, err)
	}
}

func TestSettingsAndUserWritesWrapErrorsOnClosedStore(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	s.Close()
	wantKind(t, s.UpdateSettings(ctx, map[string]string{"a": "b"}), apperr.ErrInternal)
	wantKind(t, errOf(s.GetSettings(ctx)), apperr.ErrInternal)
	wantKind(t, s.UpsertUser(ctx, "u", "x"), apperr.ErrInternal)
}

func TestUpsertUserUpdatesPassword(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	if err := s.UpsertUser(ctx, "alice", "hash1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, "alice", "hash2"); err != nil {
		t.Fatal(err)
	}
	var pw string
	if err := s.DB().QueryRowContext(ctx, `SELECT password_bcrypt FROM users WHERE username='alice'`).Scan(&pw); err != nil || pw != "hash2" {
		t.Fatalf("password = %q, %v", pw, err)
	}
}

func TestSourceMapUploadFindList(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	wantKind(t, errOf(s.UploadSourceMap(ctx, SourceMapUpload{BundleURL: "b"})), apperr.ErrInvalidInput)
	wantKind(t, errOf(s.UploadSourceMap(ctx, SourceMapUpload{Release: "r"})), apperr.ErrInvalidInput)

	up, err := s.UploadSourceMap(ctx, SourceMapUpload{Release: "1.0", Dist: "d", BundleURL: "app.js", Name: "app.js.map", Blob: []byte("one")})
	if err != nil || up.SizeBytes != 3 || up.ID == "" {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	if _, err := s.UploadSourceMap(ctx, SourceMapUpload{Release: "1.0", Dist: "d", BundleURL: "app.js", Blob: []byte("newer")}); err != nil {
		t.Fatal(err)
	}
	blob, err := s.FindSourceMap(ctx, "1.0", "d", "app.js")
	if err != nil || string(blob) != "newer" {
		t.Fatalf("FindSourceMap = %q, %v", blob, err)
	}
	blob, err = s.FindSourceMap(ctx, "9.9", "d", "app.js")
	if err != nil || blob != nil {
		t.Fatalf("missing map = %q, %v", blob, err)
	}
	list, err := s.ListSourceMaps(ctx)
	if err != nil || len(list) != 2 || list[0].SizeBytes != 5 {
		t.Fatalf("ListSourceMaps = %+v, %v", list, err)
	}
}

func TestAlertUpdateAndDelete(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := domain.WithProjectID(context.Background(), s.DefaultProjectID())
	a, err := s.CreateAlert(ctx, domain.Alert{Name: "a", Enabled: true, Condition: "new_issue"})
	if err != nil {
		t.Fatal(err)
	}
	wantKind(t, errOf(s.UpdateAlert(ctx, "bad", a)), apperr.ErrInvalidInput)
	wantKind(t, errOf(s.UpdateAlert(ctx, a.ID, domain.Alert{Name: " "})), apperr.ErrInvalidInput)

	a.Name = "renamed"
	a.Enabled = false
	a.Rule = nil
	got, err := s.UpdateAlert(ctx, a.ID, a)
	if err != nil || got.Name != "renamed" || got.Enabled {
		t.Fatalf("UpdateAlert = %+v, %v", got, err)
	}
	if err := s.DeleteAlert(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	wantKind(t, errOf(s.GetAlert(ctx, a.ID)), apperr.ErrNotFound)
	if err := s.DeleteAlert(ctx, "nonsense"); err == nil {
		t.Fatal("DeleteAlert with a malformed id returned nil")
	}
}

func TestReleaseDelete(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := domain.WithProjectID(context.Background(), s.DefaultProjectID())
	r, err := s.CreateRelease(ctx, domain.Release{Name: "v1", Environment: "prod", ObservedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRelease(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	wantKind(t, errOf(s.GetRelease(ctx, r.ID)), apperr.ErrNotFound)
	if err := s.DeleteRelease(ctx, "x"); err == nil {
		t.Fatal("malformed release id accepted")
	}
}

func TestProjectLifecycleHelpers(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()

	p, err := s.EnsureProjectPending(ctx, "fresh-app")
	if err != nil || p.Status != "pending" {
		t.Fatalf("EnsureProjectPending = %+v, %v", p, err)
	}
	again, err := s.EnsureProjectPending(ctx, "fresh-app")
	if err != nil || again.ID != p.ID {
		t.Fatalf("second EnsureProjectPending = %+v, %v", again, err)
	}
	if err := s.ApproveProject(ctx, "fresh-app"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ProjectBySlug(ctx, "fresh-app"); got.Status != "active" {
		t.Fatalf("status after approve = %q", got.Status)
	}
	wantKind(t, s.ApproveProject(ctx, "ghost"), apperr.ErrNotFound)

	if err := s.CreateAlias(ctx, "old-name", p.ID); err != nil {
		t.Fatal(err)
	}
	viaAlias, err := s.EnsureProjectPending(ctx, "old-name")
	if err != nil || viaAlias.ID != p.ID {
		t.Fatalf("alias resolve = %+v, %v", viaAlias, err)
	}
	aliases, err := s.ListAliases(ctx)
	if err != nil || len(aliases) != 1 || aliases[0].ProjectSlug != "fresh-app" {
		t.Fatalf("ListAliases = %+v, %v", aliases, err)
	}
	wantKind(t, s.DeleteProject(ctx, "ghost"), apperr.ErrNotFound)
	if err := s.DeleteProject(ctx, "fresh-app"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, errOf(s.ProjectBySlug(ctx, "fresh-app")), apperr.ErrNotFound)
}

func TestEnsureProjectPendingOnReadOnlyStoreIsNotFound(t *testing.T) {
	t.Parallel()
	ro := openReadOnlyTemp(t)
	wantKind(t, errOf(ro.EnsureProjectPending(context.Background(), "nope")), apperr.ErrNotFound)
}

func TestProjectLimitsAndSampling(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "Lim", "lim")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ProjectsWithRetentionOverride(ctx); err != nil || len(got) != 0 {
		t.Fatalf("overrides before = %v, %v", got, err)
	}
	days := 7
	if err := s.UpdateProjectLimits(ctx, "lim", &days, SamplingOn); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProjectsWithRetentionOverride(ctx)
	if err != nil || got[p.ID] != 7 {
		t.Fatalf("overrides = %v, %v", got, err)
	}

	s.SetSampleAfter(-5)
	if s.SampleAfter() != 0 {
		t.Fatalf("SampleAfter = %d, want 0", s.SampleAfter())
	}
	if got := s.sampleAfterFor(ctx, p.ID); got != defaultSampleAfter {
		t.Fatalf("opt-in with sampling disabled globally = %d, want default %d", got, defaultSampleAfter)
	}
	s.SetSampleAfter(50)
	if s.SampleAfter() != 50 || s.sampleAfterFor(ctx, p.ID) != 50 {
		t.Fatalf("opt-in threshold = %d", s.sampleAfterFor(ctx, p.ID))
	}
	if err := s.UpdateProjectLimits(ctx, "lim", nil, SamplingOff); err != nil {
		t.Fatal(err)
	}
	if got := s.sampleAfterFor(ctx, p.ID); got != 0 {
		t.Fatalf("opt-out threshold = %d, want 0", got)
	}
	if err := s.UpdateProjectLimits(ctx, "lim", nil, SamplingInherit); err != nil {
		t.Fatal(err)
	}
	if got := s.sampleAfterFor(ctx, p.ID); got != 50 {
		t.Fatalf("inherited threshold = %d, want 50", got)
	}
	if s.sampleAfterFor(ctx, 0) != 50 {
		t.Fatal("project 0 must inherit the global threshold")
	}
}

func TestProjectFunnelBarnURLAndSilenceWatch(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, "Fb", "fb"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProjectFunnelBarnURL(ctx, "fb", "https://fb.example"); err != nil {
		t.Fatal(err)
	}
	wantKind(t, s.UpdateProjectFunnelBarnURL(ctx, "ghost", "x"), apperr.ErrNotFound)

	if v, err := s.ProjectSilenceWatch(ctx, "fb"); err != nil || v != "" {
		t.Fatalf("watch = %q, %v", v, err)
	}
	wantKind(t, s.UpdateProjectSilenceWatch(ctx, "fb", "maybe"), apperr.ErrInvalidInput)
	wantKind(t, s.UpdateProjectSilenceWatch(ctx, "ghost", SilenceWatchOff), apperr.ErrNotFound)
	wantKind(t, errOf(s.ProjectSilenceWatch(ctx, "ghost")), apperr.ErrNotFound)

	before, err := s.ActiveSilenceProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProjectSilenceWatch(ctx, "fb", SilenceWatchOff); err != nil {
		t.Fatal(err)
	}
	after, err := s.ActiveSilenceProjects(ctx)
	if err != nil || len(after) != len(before)-1 {
		t.Fatalf("active before %d after %d, %v", len(before), len(after), err)
	}
	ids, err := s.ListProjectIDs(ctx)
	if err != nil || len(ids) != len(before) {
		t.Fatalf("ListProjectIDs = %v, %v", ids, err)
	}
}

func TestRecentLogTimesAndUnparseableRows(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	pid := s.DefaultProjectID()
	insert := func(ts string) {
		t.Helper()
		if _, err := s.DB().ExecContext(ctx,
			`INSERT INTO log_entries (project_id, received_at, level, message, data_json) VALUES (?, ?, 'info', 'm', '{}')`,
			pid, ts); err != nil {
			t.Fatalf("insert log: %v", err)
		}
	}
	insert("2026-01-01T00:00:01.000000000Z")
	insert("2026-01-01T00:00:02.000000000Z")
	insert("garbage")

	newest := func() error { _, _, err := s.NewestLogAt(ctx, pid); return err }
	wantKind(t, newest(), apperr.ErrInternal)
	wantKind(t, errOf(s.RecentLogTimes(ctx, pid, 3)), apperr.ErrInternal)

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM log_entries WHERE received_at='garbage'`); err != nil {
		t.Fatal(err)
	}
	times, err := s.RecentLogTimes(ctx, pid, 5)
	if err != nil || len(times) != 2 || !times[0].After(times[1]) {
		t.Fatalf("RecentLogTimes = %v, %v", times, err)
	}
	at, ok, err := s.NewestLogAt(ctx, pid)
	if err != nil || !ok || !at.Equal(times[0]) {
		t.Fatalf("NewestLogAt = %v %v %v", at, ok, err)
	}
}

func TestDeleteProjectEventsBeforeScopesToProject(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	ctx := context.Background()
	seedEventsAt(t, s, time.Now().Add(-48*time.Hour), 3)
	pid := s.DefaultProjectID()
	cutoff := time.Now().Add(-24 * time.Hour)

	for name, call := range map[string]func() (int64, error){
		"project 0":     func() (int64, error) { return s.DeleteProjectEventsBefore(ctx, 0, time.Now(), 10) },
		"limit 0":       func() (int64, error) { return s.DeleteProjectEventsBefore(ctx, pid, time.Now(), 0) },
		"other project": func() (int64, error) { return s.DeleteProjectEventsBefore(ctx, pid+99, time.Now(), 10) },
	} {
		if n, err := call(); err != nil || n != 0 {
			t.Fatalf("%s = %d, %v", name, n, err)
		}
	}
	if n, err := s.DeleteProjectEventsBefore(ctx, pid, cutoff, 2); err != nil || n != 2 {
		t.Fatalf("first batch = %d, %v", n, err)
	}
	if n, err := s.DeleteProjectEventsBefore(ctx, pid, cutoff, 2); err != nil || n != 1 {
		t.Fatalf("second batch = %d, %v", n, err)
	}
}

func TestDeleteProjectEventsBeforeReadOnlyStore(t *testing.T) {
	t.Parallel()
	ro := openReadOnlyTemp(t)
	_, err := ro.DeleteProjectEventsBefore(context.Background(), 1, time.Now(), 5)
	wantKind(t, err, apperr.ErrInternal)
}

func TestDeleteMonitorMissingIsNotFound(t *testing.T) {
	t.Parallel()
	s := openTemp(t)
	wantKind(t, s.DeleteMonitor(context.Background(), s.DefaultProjectID(), "nope"), apperr.ErrNotFound)
}

func TestIsDatabaseLockedStringFallback(t *testing.T) {
	t.Parallel()
	if !IsDatabaseLocked(errors.New("database is locked")) || IsDatabaseLocked(errors.New("other")) {
		t.Fatal("IsDatabaseLocked string fallback wrong")
	}
}

func TestCheckpointPolicyAliases(t *testing.T) {
	t.Parallel()
	if DefaultCheckpointPolicy().TruncateEvery <= LegacyCheckpointPolicy().TruncateEvery {
		t.Fatal("default policy truncates no less often than the legacy one")
	}
	if _, err := WALFileSize(filepath.Join(t.TempDir(), "none.db"))(); err != nil {
		t.Fatalf("a missing wal file is size 0, got error %v", err)
	}
}

func TestParseIssueIDForms(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "  ", "issue-abc", "nodash", "-5", "BW-x"} {
		_, _, err := parseIssueID(bad)
		wantKind(t, err, apperr.ErrInvalidInput)
	}
	if p, n, err := parseIssueID("issue-000012"); err != nil || p != "" || n != 12 {
		t.Fatalf("legacy = %q %d %v", p, n, err)
	}
	if p, n, err := parseIssueID("BW-3"); err != nil || p != "BW" || n != 3 {
		t.Fatalf("display = %q %d %v", p, n, err)
	}
}
