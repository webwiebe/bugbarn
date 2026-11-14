package keyrequests

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// failingRepo wraps fakeRepo and fails the calls named in its fields.
type failingRepo struct {
	*fakeRepo
	countErr, createErr, purgeErr, claimErr, resolveErr, listErr error
}

func (f *failingRepo) CountPendingKeyRequests(ctx context.Context, id int64, now time.Time) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.fakeRepo.CountPendingKeyRequests(ctx, id, now)
}

func (f *failingRepo) CreateKeyRequest(ctx context.Context, kr domain.KeyRequest, sha string) error {
	if f.createErr != nil {
		return f.createErr
	}
	return f.fakeRepo.CreateKeyRequest(ctx, kr, sha)
}

func (f *failingRepo) PurgeKeyRequests(context.Context, time.Time) error { return f.purgeErr }

func (f *failingRepo) ClaimKeyRequest(ctx context.Context, id, name, sha string, now time.Time) error {
	if f.claimErr != nil {
		return f.claimErr
	}
	return f.fakeRepo.ClaimKeyRequest(ctx, id, name, sha, now)
}

func (f *failingRepo) ResolveKeyRequest(ctx context.Context, id, status string, now time.Time) error {
	if f.resolveErr != nil {
		return f.resolveErr
	}
	return f.fakeRepo.ResolveKeyRequest(ctx, id, status, now)
}

func (f *failingRepo) ListPendingKeyRequests(context.Context, time.Time) ([]domain.KeyRequest, error) {
	return []domain.KeyRequest{{ID: "p1"}}, f.listErr
}

func loggedService(repo Repository) (*Service, *bytes.Buffer) {
	var buf bytes.Buffer
	return New(repo, slog.New(slog.NewJSONHandler(&buf, nil))), &buf
}

func TestNewDefaultsLogger(t *testing.T) {
	if New(newFakeRepo(), nil).logger == nil {
		t.Fatal("expected a default logger")
	}
}

func TestCreateCountFailurePassesThrough(t *testing.T) {
	svc, _ := loggedService(&failingRepo{fakeRepo: newFakeRepo(), countErr: apperr.Internal("db", nil)})
	if _, err := svc.Create(context.Background(), geo, "x"); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateStoreFailureLogsError(t *testing.T) {
	svc, buf := loggedService(&failingRepo{fakeRepo: newFakeRepo(), createErr: apperr.Internal("db", nil)})
	if _, err := svc.Create(context.Background(), geo, "x"); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestCreateSucceedsWhenPurgeFails(t *testing.T) {
	svc, buf := loggedService(&failingRepo{fakeRepo: newFakeRepo(), purgeErr: errors.New("locked")})
	created, err := svc.Create(context.Background(), geo, "x")
	if err != nil || created.ID == "" || created.PollSecret == "" {
		t.Fatalf("Create = %+v, %v", created, err)
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) || strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("purge failure should warn only: %s", buf.String())
	}
}

func TestPollUnknownIDIsNotFound(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.Poll(context.Background(), "geo", "missing", "secret"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPollClaimConflictReportsClaimed(t *testing.T) {
	repo := &failingRepo{fakeRepo: newFakeRepo()}
	svc, buf := loggedService(repo)
	ctx := context.Background()
	c, err := svc.Create(ctx, geo, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve(ctx, c.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	repo.claimErr = apperr.Conflict("lost the race", nil)
	buf.Reset()
	res, err := svc.Poll(ctx, "geo", c.ID, c.PollSecret)
	if err != nil || res.Status != domain.KeyRequestClaimed || res.APIKey != "" {
		t.Fatalf("Poll = %+v, %v", res, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("losing the race should log nothing: %s", buf.String())
	}
}

func TestPollClaimFailureLogsError(t *testing.T) {
	repo := &failingRepo{fakeRepo: newFakeRepo()}
	svc, buf := loggedService(repo)
	ctx := context.Background()
	c, _ := svc.Create(ctx, geo, "x")
	if err := svc.Approve(ctx, c.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	repo.claimErr = apperr.Internal("db", nil)
	buf.Reset()
	if _, err := svc.Poll(ctx, "geo", c.ID, c.PollSecret); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestListPending(t *testing.T) {
	svc, _ := loggedService(&failingRepo{fakeRepo: newFakeRepo()})
	got, err := svc.ListPending(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("ListPending = %v, %v", got, err)
	}
}

func TestResolveErrors(t *testing.T) {
	repo := &failingRepo{fakeRepo: newFakeRepo()}
	svc, buf := loggedService(repo)
	ctx := context.Background()
	if err := svc.Deny(ctx, "missing", "admin"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("unknown id err = %v", err)
	}
	c, _ := svc.Create(ctx, geo, "x")
	buf.Reset()

	repo.resolveErr = apperr.Conflict("already resolved", nil)
	if err := svc.Approve(ctx, c.ID, "admin"); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("conflict err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("a conflict is the caller's problem and logs nothing: %s", buf.String())
	}

	repo.resolveErr = apperr.Internal("db", nil)
	if err := svc.Deny(ctx, c.ID, "admin"); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("internal err = %v", err)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestDenyRecordsStatusAndActor(t *testing.T) {
	repo := &failingRepo{fakeRepo: newFakeRepo()}
	svc, buf := loggedService(repo)
	ctx := context.Background()
	c, _ := svc.Create(ctx, geo, "x")
	if err := svc.Deny(ctx, c.ID, "wiebe"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if repo.rows[c.ID].Status != domain.KeyRequestDenied {
		t.Fatalf("status = %s", repo.rows[c.ID].Status)
	}
	if !strings.Contains(buf.String(), "key request denied") || !strings.Contains(buf.String(), `"actor":"wiebe"`) {
		t.Fatalf("log = %s", buf.String())
	}
}
