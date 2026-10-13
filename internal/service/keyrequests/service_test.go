package keyrequests

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	rows   map[string]*domain.KeyRequest
	polls  map[string]string
	minted []string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]*domain.KeyRequest{}, polls: map[string]string{}}
}

func (f *fakeRepo) CreateKeyRequest(_ context.Context, kr domain.KeyRequest, pollSHA string) error {
	kr.Status = domain.KeyRequestPending
	f.rows[kr.ID] = &kr
	f.polls[kr.ID] = pollSHA
	return nil
}

func (f *fakeRepo) CountPendingKeyRequests(_ context.Context, projectID int64, _ time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.ProjectID == projectID && r.Status == domain.KeyRequestPending {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) GetKeyRequest(_ context.Context, id string) (domain.KeyRequest, string, error) {
	r, ok := f.rows[id]
	if !ok {
		return domain.KeyRequest{}, "", apperr.NotFound("not found", nil)
	}
	return *r, f.polls[id], nil
}

func (f *fakeRepo) ListPendingKeyRequests(context.Context, time.Time) ([]domain.KeyRequest, error) {
	return nil, nil
}

func (f *fakeRepo) ResolveKeyRequest(_ context.Context, id, status string, _ time.Time) error {
	r, ok := f.rows[id]
	if !ok || r.Status != domain.KeyRequestPending {
		return apperr.Conflict("not pending", nil)
	}
	r.Status = status
	return nil
}

func (f *fakeRepo) ClaimKeyRequest(_ context.Context, id, _, keySHA string, _ time.Time) error {
	r, ok := f.rows[id]
	if !ok || r.Status != domain.KeyRequestApproved {
		return apperr.Conflict("not claimable", nil)
	}
	r.Status = domain.KeyRequestClaimed
	f.minted = append(f.minted, keySHA)
	return nil
}

func (f *fakeRepo) PurgeKeyRequests(context.Context, time.Time) error { return nil }

func newService() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	return New(repo, slog.New(slog.DiscardHandler)), repo
}

var geo = domain.Project{ID: 1, Slug: "geo"}

func TestCreateValidatesLabel(t *testing.T) {
	svc, _ := newService()
	for _, bad := range []string{"", "   ", strings.Repeat("a", 101), "line\nbreak"} {
		if _, err := svc.Create(context.Background(), geo, bad); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("label %q: %v", bad, err)
		}
	}
	if _, err := svc.Create(context.Background(), geo, "  org/repo prod "); err != nil {
		t.Fatal(err)
	}
}

func TestPollRejectsWrongSecretOrSlug(t *testing.T) {
	svc, _ := newService()
	c, err := svc.Create(context.Background(), geo, "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ slug, secret string }{{"geo", "wrong"}, {"other", c.PollSecret}} {
		if _, err := svc.Poll(context.Background(), tc.slug, c.ID, tc.secret); !errors.Is(err, apperr.ErrNotFound) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func TestApprovedKeyIsReturnedOnceAndOnlyItsHashIsStored(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	c, _ := svc.Create(ctx, geo, "x")
	if err := svc.Approve(ctx, c.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Poll(ctx, "geo", c.ID, c.PollSecret)
	if err != nil || first.APIKey == "" || first.Status != domain.KeyRequestApproved {
		t.Fatalf("first poll = %+v err %v", first, err)
	}
	if len(repo.minted) != 1 || repo.minted[0] != sha256Hex(first.APIKey) || repo.minted[0] == first.APIKey {
		t.Fatalf("stored %v for key %q", repo.minted, first.APIKey)
	}
	second, err := svc.Poll(ctx, "geo", c.ID, c.PollSecret)
	if err != nil || second.APIKey != "" || second.Status != domain.KeyRequestClaimed {
		t.Fatalf("second poll = %+v err %v", second, err)
	}
}

func TestDeniedAndPendingNeverReturnAKey(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	c, _ := svc.Create(ctx, geo, "x")
	if res, _ := svc.Poll(ctx, "geo", c.ID, c.PollSecret); res.Status != domain.KeyRequestPending || res.APIKey != "" {
		t.Fatalf("pending = %+v", res)
	}
	if err := svc.Deny(ctx, c.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if res, _ := svc.Poll(ctx, "geo", c.ID, c.PollSecret); res.Status != domain.KeyRequestDenied || res.APIKey != "" {
		t.Fatalf("denied = %+v", res)
	}
	if err := svc.Approve(ctx, c.ID, "admin"); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("approve after deny: %v", err)
	}
}

func TestExpiredRequestIsGoneEvenWhenApproved(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	c, _ := svc.Create(ctx, geo, "x")
	if err := svc.Approve(ctx, c.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return time.Now().Add(RequestTTL + time.Minute) })
	res, err := svc.Poll(ctx, "geo", c.ID, c.PollSecret)
	if err != nil || res.Status != domain.KeyRequestExpired || res.APIKey != "" || len(repo.minted) != 0 {
		t.Fatalf("res = %+v err %v minted %v", res, err, repo.minted)
	}
}

func TestPendingCap(t *testing.T) {
	svc, _ := newService()
	for range MaxPendingPerProject {
		if _, err := svc.Create(context.Background(), geo, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(context.Background(), geo, "x"); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("over the cap: %v", err)
	}
}
