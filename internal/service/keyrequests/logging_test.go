package keyrequests

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

// errRepo fails the repository calls named in its fields.
type errRepo struct {
	*fakeRepo
	get, count, create, list, resolve, claim error
}

func (r *errRepo) GetKeyRequest(ctx context.Context, id string) (domain.KeyRequest, string, error) {
	if r.get != nil {
		return domain.KeyRequest{}, "", r.get
	}
	return r.fakeRepo.GetKeyRequest(ctx, id)
}

func (r *errRepo) CountPendingKeyRequests(ctx context.Context, id int64, now time.Time) (int, error) {
	if r.count != nil {
		return 0, r.count
	}
	return r.fakeRepo.CountPendingKeyRequests(ctx, id, now)
}

func (r *errRepo) CreateKeyRequest(ctx context.Context, kr domain.KeyRequest, sha string) error {
	if r.create != nil {
		return r.create
	}
	return r.fakeRepo.CreateKeyRequest(ctx, kr, sha)
}

func (r *errRepo) ListPendingKeyRequests(ctx context.Context, now time.Time) ([]domain.KeyRequest, error) {
	if r.list != nil {
		return nil, r.list
	}
	return r.fakeRepo.ListPendingKeyRequests(ctx, now)
}

func (r *errRepo) ResolveKeyRequest(ctx context.Context, id, status string, now time.Time) error {
	if r.resolve != nil {
		return r.resolve
	}
	return r.fakeRepo.ResolveKeyRequest(ctx, id, status, now)
}

func (r *errRepo) ClaimKeyRequest(ctx context.Context, id, name, sha string, now time.Time) error {
	if r.claim != nil {
		return r.claim
	}
	return r.fakeRepo.ClaimKeyRequest(ctx, id, name, sha, now)
}

func TestLoggingRule(t *testing.T) {
	ctx := context.Background()
	// withRepo runs f against a service whose repo fails as configured by set.
	withRepo := func(set func(*errRepo, error), f func(*Service, *errRepo)) func(*slog.Logger, error) {
		return func(log *slog.Logger, err error) {
			repo := &errRepo{fakeRepo: newFakeRepo()}
			svc := New(repo, log)
			// Seed a pending request, then arm the failure.
			c, cerr := svc.Create(ctx, geo, "seed")
			if cerr != nil {
				t.Fatal(cerr)
			}
			repo.rows[c.ID].Status = domain.KeyRequestApproved
			repo.polls[c.ID] = sha256Hex("secret")
			set(repo, err)
			f(svc, repo)
		}
	}
	firstRow := func(r *errRepo) string {
		for id := range r.rows {
			return id
		}
		return ""
	}
	logtest.CheckRule(t, []logtest.Call{
		{Name: "Create count", Run: withRepo(func(r *errRepo, e error) { r.count = e }, func(s *Service, _ *errRepo) { _, _ = s.Create(ctx, geo, "x") })},
		{Name: "Create insert", Run: withRepo(func(r *errRepo, e error) { r.create = e }, func(s *Service, _ *errRepo) { _, _ = s.Create(ctx, geo, "x") })},
		{Name: "Poll get", Run: withRepo(func(r *errRepo, e error) { r.get = e }, func(s *Service, r *errRepo) { _, _ = s.Poll(ctx, "geo", firstRow(r), "secret") })},
		{Name: "Poll claim", Run: withRepo(func(r *errRepo, e error) { r.claim = e }, func(s *Service, r *errRepo) { _, _ = s.Poll(ctx, "geo", firstRow(r), "secret") })},
		{Name: "ListPending", Run: withRepo(func(r *errRepo, e error) { r.list = e }, func(s *Service, _ *errRepo) { _, _ = s.ListPending(ctx) })},
		{Name: "Approve get", Run: withRepo(func(r *errRepo, e error) { r.get = e }, func(s *Service, r *errRepo) { _ = s.Approve(ctx, firstRow(r), "admin") })},
		{Name: "Deny resolve", Run: withRepo(func(r *errRepo, e error) { r.resolve = e }, func(s *Service, r *errRepo) { _ = s.Deny(ctx, firstRow(r), "admin") })},
	})
}
