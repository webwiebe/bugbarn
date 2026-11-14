package projects

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	run := func(f func(*Service)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) { f(New(&fakeRepo{err: err}, log)) }
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "List", Run: run(func(s *Service) { _, _ = s.List(ctx) })},
		{Name: "Create", Run: run(func(s *Service) { _, _ = s.Create(ctx, "n", "s") })},
		{Name: "Ensure", Run: run(func(s *Service) { _, _ = s.Ensure(ctx, "s") })},
		{Name: "EnsurePending", Run: run(func(s *Service) { _, _ = s.EnsurePending(ctx, "s") })},
		{Name: "Delete", Run: run(func(s *Service) { _ = s.Delete(ctx, "s") })},
		{Name: "Approve", Run: run(func(s *Service) { _ = s.Approve(ctx, "s") })},
		{Name: "BySlug", Run: run(func(s *Service) { _, _ = s.BySlug(ctx, "s") })},
		{Name: "ListAPIKeys", Run: run(func(s *Service) { _, _ = s.ListAPIKeys(ctx) })},
		{Name: "CreateAPIKey", Run: run(func(s *Service) { _, _ = s.CreateAPIKey(ctx, "n", 1, "h", "ingest") })},
		{Name: "DeleteAPIKey", Run: run(func(s *Service) { _ = s.DeleteAPIKey(ctx, 1) })},
		{Name: "EnsureSetupAPIKey", Run: run(func(s *Service) { _ = s.EnsureSetupAPIKey(ctx, "n", 1, "h") })},
		{Name: "ValidAPIKeySHA256", Run: run(func(s *Service) { _, _, _, _ = s.ValidAPIKeySHA256(ctx, "h") })},
		{Name: "TouchAPIKey", Run: run(func(s *Service) { _ = s.TouchAPIKey(ctx, "h") })},
		{Name: "GetSettings", Run: run(func(s *Service) { _, _ = s.GetSettings(ctx) })},
		{Name: "UpdateSettings", Run: run(func(s *Service) { _ = s.UpdateSettings(ctx, map[string]string{"a": "b"}) })},
		{Name: "CreateAlias", Run: run(func(s *Service) { _ = s.CreateAlias(ctx, "a", 1) })},
		{Name: "DeleteAlias", Run: run(func(s *Service) { _ = s.DeleteAlias(ctx, "a") })},
		{Name: "ResolveAlias", Run: func(log *slog.Logger, err error) {
			_, _ = New(&resolveFailRepo{err: err}, log).ResolveAlias(ctx, "a")
		}},
		{Name: "ListAliases", Run: run(func(s *Service) { _, _ = s.ListAliases(ctx) })},
		{Name: "Rename", Run: run(func(s *Service) { _ = s.Rename(ctx, "a", "b", "B") })},
		{Name: "UpdateLimits", Run: run(func(s *Service) { _ = s.UpdateLimits(ctx, "a", nil, "") })},
		{Name: "Merge", Run: run(func(s *Service) { _ = s.Merge(ctx, "a", "b") })},
		{Name: "CreateGroup", Run: run(func(s *Service) { _, _ = s.CreateGroup(ctx, "n", "s") })},
		{Name: "ListGroups", Run: run(func(s *Service) { _, _ = s.ListGroups(ctx) })},
		{Name: "DeleteGroup", Run: run(func(s *Service) { _ = s.DeleteGroup(ctx, "s") })},
		{Name: "AssignProjectToGroup", Run: run(func(s *Service) { _ = s.AssignProjectToGroup(ctx, "p", "g") })},
		{Name: "RemoveProjectFromGroup", Run: run(func(s *Service) { _ = s.RemoveProjectFromGroup(ctx, "p") })},
		{Name: "ListGroupProjects", Run: run(func(s *Service) { _, _ = s.ListGroupProjects(ctx, "g") })},
		{Name: "SilenceWatch", Run: run(func(s *Service) { _, _ = s.SilenceWatch(ctx, "s") })},
		{Name: "UpdateSilenceWatch", Run: run(func(s *Service) { _, _ = s.UpdateSilenceWatch(ctx, "s", "off") })},
		{Name: "UpdateFunnelBarnURL", Run: run(func(s *Service) { _, _ = s.UpdateFunnelBarnURL(ctx, "s", "") })},
		{Name: "APIKey", Run: run(func(s *Service) { _, _ = s.APIKey(ctx, 1) })},
		{Name: "ListSetupKeyRevocations", Run: run(func(s *Service) { _, _ = s.ListSetupKeyRevocations(ctx) })},
		{Name: "RevokeSetupKey", Run: run(func(s *Service) { _ = s.RevokeSetupKey(ctx, "s", "h") })},
		{Name: "UsageAll", Run: run(func(s *Service) { _, _, _ = s.UsageAll(ctx) })},
	})
}

// resolveFailRepo fails ResolveAlias with its own error; fakeRepo always answers NotFound.
type resolveFailRepo struct {
	fakeRepo
	err error
}

func (r *resolveFailRepo) ResolveAlias(context.Context, string) (int64, error) { return 0, r.err }

func TestUpdateSilenceWatchValidatesInTheService(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo, nil)
	for _, bad := range []string{"on", "off ish", "true"} {
		if _, err := svc.UpdateSilenceWatch(context.Background(), "s", bad); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%q: err = %v, want invalid input", bad, err)
		}
	}
	if repo.silenceSlug != "" {
		t.Errorf("repo was called for an invalid value: %q", repo.silenceSlug)
	}
	for _, ok := range []string{"", "  ", " off "} {
		if _, err := svc.UpdateSilenceWatch(context.Background(), "s", ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}
