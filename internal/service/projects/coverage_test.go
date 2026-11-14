package projects

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// dataRepo adds canned results on top of fakeRepo's failure behavior.
type dataRepo struct {
	fakeRepo
	deleteErr error
}

func (d *dataRepo) ListProjects(context.Context) ([]domain.Project, error) {
	return []domain.Project{{Slug: "a"}}, d.err
}
func (d *dataRepo) EnsureProject(_ context.Context, slug string) (domain.Project, error) {
	return domain.Project{Slug: slug}, d.err
}
func (d *dataRepo) EnsureProjectPending(_ context.Context, slug string) (domain.Project, error) {
	return domain.Project{Slug: slug, Status: "pending"}, d.err
}
func (d *dataRepo) DeleteProject(context.Context, string) error { return d.deleteErr }
func (d *dataRepo) ListAPIKeys(context.Context) ([]domain.APIKey, error) {
	return []domain.APIKey{{ID: 1}}, d.err
}
func (d *dataRepo) CreateAPIKey(_ context.Context, name string, pid int64, _, _ string) (domain.APIKey, error) {
	return domain.APIKey{ID: 5, Name: name, ProjectID: pid}, d.err
}
func (d *dataRepo) ValidAPIKeySHA256(context.Context, string) (int64, string, bool, error) {
	return 3, "ingest", true, d.err
}
func (d *dataRepo) APIKeyByID(_ context.Context, id int64) (domain.APIKey, error) {
	return domain.APIKey{ID: id}, d.err
}
func (d *dataRepo) ListSetupKeyRevocations(context.Context) ([]domain.SetupKeyRevocation, error) {
	return []domain.SetupKeyRevocation{{}}, d.err
}
func (d *dataRepo) GetSettings(context.Context) (map[string]string, error) {
	return map[string]string{"k": "v"}, d.err
}
func (d *dataRepo) ResolveAlias(context.Context, string) (int64, error) { return 9, d.err }
func (d *dataRepo) ListAliases(context.Context) ([]domain.ProjectAlias, error) {
	return []domain.ProjectAlias{{}}, d.err
}
func (d *dataRepo) CreateGroup(_ context.Context, name, slug string) (domain.ProjectGroup, error) {
	return domain.ProjectGroup{ID: 4, Name: name, Slug: slug}, d.err
}
func (d *dataRepo) ListGroups(context.Context) ([]domain.ProjectGroup, error) {
	return []domain.ProjectGroup{{}}, d.err
}
func (d *dataRepo) ListGroupProjects(context.Context, string) ([]domain.Project, error) {
	return []domain.Project{{Slug: "g"}}, d.err
}

func newJSONLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestReadMethodsReturnRepoData(t *testing.T) {
	t.Parallel()
	svc := New(&dataRepo{}, nil)
	ctx := context.Background()

	if got, err := svc.List(ctx); err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got, err := svc.Ensure(ctx, "x"); err != nil || got.Slug != "x" {
		t.Fatalf("Ensure = %+v, %v", got, err)
	}
	if got, err := svc.EnsurePending(ctx, "x"); err != nil || got.Status != "pending" {
		t.Fatalf("EnsurePending = %+v, %v", got, err)
	}
	if got, err := svc.ListAPIKeys(ctx); err != nil || len(got) != 1 {
		t.Fatalf("ListAPIKeys = %v, %v", got, err)
	}
	if got, err := svc.APIKey(ctx, 8); err != nil || got.ID != 8 {
		t.Fatalf("APIKey = %+v, %v", got, err)
	}
	if pid, scope, found, err := svc.ValidAPIKeySHA256(ctx, "h"); err != nil || pid != 3 || scope != "ingest" || !found {
		t.Fatalf("ValidAPIKeySHA256 = %d %q %v %v", pid, scope, found, err)
	}
	if got, err := svc.ListSetupKeyRevocations(ctx); err != nil || len(got) != 1 {
		t.Fatalf("ListSetupKeyRevocations = %v, %v", got, err)
	}
	if got, err := svc.GetSettings(ctx); err != nil || got["k"] != "v" {
		t.Fatalf("GetSettings = %v, %v", got, err)
	}
	if id, err := svc.ResolveAlias(ctx, "old"); err != nil || id != 9 {
		t.Fatalf("ResolveAlias = %d, %v", id, err)
	}
	if got, err := svc.ListAliases(ctx); err != nil || len(got) != 1 {
		t.Fatalf("ListAliases = %v, %v", got, err)
	}
	if got, err := svc.ListGroups(ctx); err != nil || len(got) != 1 {
		t.Fatalf("ListGroups = %v, %v", got, err)
	}
	if got, err := svc.ListGroupProjects(ctx, "g"); err != nil || got[0].Slug != "g" {
		t.Fatalf("ListGroupProjects = %v, %v", got, err)
	}
	if svc.DefaultProjectID() != 1 || svc.SampleAfter() != 1000 {
		t.Fatalf("repo constants not forwarded")
	}
}

func TestWritesLogOutcomes(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	repo := &dataRepo{fakeRepo: fakeRepo{projects: map[string]domain.Project{}}}
	svc := New(repo, logger)
	ctx := context.Background()

	if got, err := svc.CreateAPIKey(ctx, "ci", 2, "sha", "ingest"); err != nil || got.ProjectID != 2 {
		t.Fatalf("CreateAPIKey = %+v, %v", got, err)
	}
	if got, err := svc.CreateGroup(ctx, "Team", "team"); err != nil || got.Slug != "team" {
		t.Fatalf("CreateGroup = %+v, %v", got, err)
	}
	steps := map[string]error{
		"DeleteAPIKey":           svc.DeleteAPIKey(ctx, 1),
		"EnsureSetupAPIKey":      svc.EnsureSetupAPIKey(ctx, "n", 1, "sha"),
		"TouchAPIKey":            svc.TouchAPIKey(ctx, "sha"),
		"UpdateSettings":         svc.UpdateSettings(ctx, map[string]string{"a": "b"}),
		"CreateAlias":            svc.CreateAlias(ctx, "old", 1),
		"DeleteAlias":            svc.DeleteAlias(ctx, "old"),
		"Rename":                 svc.Rename(ctx, "a", "b", "B"),
		"Merge":                  svc.Merge(ctx, "a", "b"),
		"DeleteGroup":            svc.DeleteGroup(ctx, "g"),
		"AssignProjectToGroup":   svc.AssignProjectToGroup(ctx, "p", "g"),
		"RemoveProjectFromGroup": svc.RemoveProjectFromGroup(ctx, "p"),
		"RevokeSetupKey":         svc.RevokeSetupKey(ctx, "p", "sha"),
	}
	for name, err := range steps {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, msg := range []string{"api key created", "group created", "alias created", "alias deleted",
		"project renamed", "projects merged", "group deleted", "project assigned to group",
		"project removed from group", "setup key revoked"} {
		if !strings.Contains(buf.String(), msg) {
			t.Errorf("missing log line %q", msg)
		}
	}
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Errorf("successful writes logged an error: %s", buf.String())
	}
}

func TestUpdateLimitsLogsRetentionAndPassesValuesThrough(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	repo := &fakeRepo{}
	svc := New(repo, logger)
	days := 14
	if err := svc.UpdateLimits(context.Background(), "p", &days, "adaptive"); err != nil {
		t.Fatalf("UpdateLimits: %v", err)
	}
	if repo.limitsSlug != "p" || *repo.limitsRetention != 14 || repo.limitsSampling != "adaptive" {
		t.Fatalf("repo saw %q %v %q", repo.limitsSlug, repo.limitsRetention, repo.limitsSampling)
	}
	if !strings.Contains(buf.String(), `"retention_days":"14"`) {
		t.Fatalf("log = %s", buf.String())
	}
	buf.Reset()
	if err := svc.UpdateLimits(context.Background(), "p", nil, ""); err != nil {
		t.Fatalf("UpdateLimits nil: %v", err)
	}
	if !strings.Contains(buf.String(), `"retention_days":"inherit"`) {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestSilenceWatchReadsThroughRepo(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{silenceValue: "off"}
	svc := New(repo, nil)
	if got, err := svc.SilenceWatch(context.Background(), "p"); err != nil || got != "off" {
		t.Fatalf("SilenceWatch = %q, %v", got, err)
	}
}

func TestDeleteIsIdempotentOnNotFound(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	svc := New(&dataRepo{deleteErr: apperr.NotFound("gone", nil)}, logger)
	if err := svc.Delete(context.Background(), "gone"); err != nil {
		t.Fatalf("Delete of a missing project = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "project already deleted") || strings.Contains(buf.String(), "ERROR") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestDeleteSurfacesOtherErrors(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	svc := New(&dataRepo{deleteErr: apperr.Conflict("has events", nil)}, logger)
	if err := svc.Delete(context.Background(), "p"); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("Delete err = %v", err)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("failure not logged: %s", buf.String())
	}
	svc = New(&dataRepo{fakeRepo: fakeRepo{projects: map[string]domain.Project{"p": {}}}}, nil)
	if err := svc.Delete(context.Background(), "p"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestApproveLogsLookupFailureButSucceeds(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	// ApproveProject passes (project present), then the replay lookup fails.
	repo := &lookupFailRepo{fakeRepo: fakeRepo{projects: map[string]domain.Project{"svc": {ID: 1}}}}
	svc := New(repo, logger)
	rep := &fakeReplayer{}
	svc.SetHeldReplayer(rep)
	if err := svc.Approve(context.Background(), "svc"); err != nil {
		t.Fatalf("Approve = %v, want nil", err)
	}
	if rep.calls != 0 {
		t.Fatalf("replayer ran after a failed lookup")
	}
	if !strings.Contains(buf.String(), "approve: lookup project for replay") {
		t.Fatalf("log = %s", buf.String())
	}
}

type lookupFailRepo struct{ fakeRepo }

func (l *lookupFailRepo) ProjectBySlug(context.Context, string) (domain.Project, error) {
	return domain.Project{}, apperr.Internal("db", nil)
}

type countingReplayer struct{ n int }

func (c countingReplayer) ReplayHeld(context.Context, int64) (int, error) { return c.n, nil }

func TestApproveLogsReplayCount(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	repo := &fakeRepo{projects: map[string]domain.Project{"svc": {ID: 1}}}
	svc := New(repo, logger)
	svc.SetHeldReplayer(countingReplayer{n: 3})
	if err := svc.Approve(context.Background(), "svc"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !strings.Contains(buf.String(), "replayed held events on approval") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestBySlugNotFoundIsNotLogged(t *testing.T) {
	t.Parallel()
	logger, buf := newJSONLogger()
	svc := New(&fakeRepo{projects: map[string]domain.Project{}}, logger)
	if _, err := svc.BySlug(context.Background(), "nope"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("not-found logged: %s", buf.String())
	}
	svc = New(&fakeRepo{projects: map[string]domain.Project{"a": {Slug: "a"}}}, logger)
	if got, err := svc.BySlug(context.Background(), "a"); err != nil || got.Slug != "a" {
		t.Fatalf("BySlug = %+v, %v", got, err)
	}
}

// Every write path keeps the repo's apperr kind and logs at ERROR except the
// ones that the service passes through silently (listed in silent).
func TestFailuresKeepKindAndLogBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	days := 3
	type call func(*Service) error
	logged := map[string]call{
		"create":         func(s *Service) error { _, err := s.Create(ctx, "N", "n"); return err },
		"approve":        func(s *Service) error { return s.Approve(ctx, "n") },
		"bySlug":         func(s *Service) error { _, err := s.BySlug(ctx, "n"); return err },
		"createAPIKey":   func(s *Service) error { _, err := s.CreateAPIKey(ctx, "n", 1, "h", "s"); return err },
		"deleteAPIKey":   func(s *Service) error { return s.DeleteAPIKey(ctx, 1) },
		"updateSettings": func(s *Service) error { return s.UpdateSettings(ctx, nil) },
		"createAlias":    func(s *Service) error { return s.CreateAlias(ctx, "a", 1) },
		"deleteAlias":    func(s *Service) error { return s.DeleteAlias(ctx, "a") },
		"rename":         func(s *Service) error { return s.Rename(ctx, "a", "b", "B") },
		"merge":          func(s *Service) error { return s.Merge(ctx, "a", "b") },
		"updateLimits":   func(s *Service) error { return s.UpdateLimits(ctx, "a", &days, "") },
		"createGroup":    func(s *Service) error { _, err := s.CreateGroup(ctx, "G", "g"); return err },
		"deleteGroup":    func(s *Service) error { return s.DeleteGroup(ctx, "g") },
		"assign":         func(s *Service) error { return s.AssignProjectToGroup(ctx, "p", "g") },
		"remove":         func(s *Service) error { return s.RemoveProjectFromGroup(ctx, "p") },
		"revokeSetupKey": func(s *Service) error { return s.RevokeSetupKey(ctx, "p", "h") },
		"silenceWatch":   func(s *Service) error { _, err := s.UpdateSilenceWatch(ctx, "p", "off"); return err },
		"funnelBarn":     func(s *Service) error { _, err := s.UpdateFunnelBarnURL(ctx, "p", "https://f.example"); return err },
	}
	silent := map[string]call{
		"list":           func(s *Service) error { _, err := s.List(ctx); return err },
		"ensure":         func(s *Service) error { _, err := s.Ensure(ctx, "n"); return err },
		"ensurePending":  func(s *Service) error { _, err := s.EnsurePending(ctx, "n"); return err },
		"listAPIKeys":    func(s *Service) error { _, err := s.ListAPIKeys(ctx); return err },
		"ensureSetupKey": func(s *Service) error { return s.EnsureSetupAPIKey(ctx, "n", 1, "h") },
		"validKey":       func(s *Service) error { _, _, _, err := s.ValidAPIKeySHA256(ctx, "h"); return err },
		"touchKey":       func(s *Service) error { return s.TouchAPIKey(ctx, "h") },
		"getSettings":    func(s *Service) error { _, err := s.GetSettings(ctx); return err },
		"listAliases":    func(s *Service) error { _, err := s.ListAliases(ctx); return err },
		"listGroups":     func(s *Service) error { _, err := s.ListGroups(ctx); return err },
		"groupProjects":  func(s *Service) error { _, err := s.ListGroupProjects(ctx, "g"); return err },
		"apiKey":         func(s *Service) error { _, err := s.APIKey(ctx, 1); return err },
		"revocations":    func(s *Service) error { _, err := s.ListSetupKeyRevocations(ctx); return err },
		"silenceRead":    func(s *Service) error { _, err := s.SilenceWatch(ctx, "p"); return err },
	}
	cause := apperr.Conflict("clash", nil)
	for name, c := range logged {
		logger, buf := newJSONLogger()
		err := c(New(&fakeRepo{err: cause}, logger))
		if !errors.Is(err, apperr.ErrConflict) {
			t.Errorf("%s: err = %v, want conflict", name, err)
		}
		if !strings.Contains(buf.String(), `"level":"ERROR"`) {
			t.Errorf("%s: failure not logged at error level", name)
		}
	}
	for name, c := range silent {
		logger, buf := newJSONLogger()
		err := c(New(&fakeRepo{err: cause}, logger))
		if !errors.Is(err, apperr.ErrConflict) {
			t.Errorf("%s: err = %v, want conflict", name, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: unexpected log output: %s", name, buf.String())
		}
	}
}
