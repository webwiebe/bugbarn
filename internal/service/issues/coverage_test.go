package issues

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

// dataRepo returns canned data when no error is set; fakeRepo supplies the
// failure behavior.
type dataRepo struct {
	fakeRepo
}

func (d *dataRepo) ListIssues(context.Context) ([]domain.Issue, error) {
	if d.err != nil {
		return nil, d.err
	}
	return []domain.Issue{{ID: "a"}, {ID: "b"}}, nil
}

func (d *dataRepo) ListIssuesFiltered(context.Context, domain.IssueFilter) ([]domain.Issue, error) {
	return d.ListIssues(context.Background())
}

func (d *dataRepo) ResolveIssue(_ context.Context, id string) (domain.Issue, error) {
	return domain.Issue{ID: id, Status: "resolved"}, d.err
}

func (d *dataRepo) HourlyEventCounts(context.Context, []int64) (map[int64][24]int, error) {
	if d.err != nil {
		return nil, d.err
	}
	return map[int64][24]int{1: {3: 9}}, nil
}

func (d *dataRepo) IssueRowIDByDisplayID(context.Context, string) (int64, error) {
	return 77, d.err
}

func (d *dataRepo) ListIssueEvents(context.Context, string, int, int64) ([]domain.Event, bool, error) {
	if d.err != nil {
		return nil, false, d.err
	}
	return []domain.Event{{ID: "e1"}}, true, nil
}

func (d *dataRepo) IssueReleases(context.Context, string, int) ([]domain.IssueRelease, int, error) {
	if d.err != nil {
		return nil, 0, d.err
	}
	return []domain.IssueRelease{{}}, 12, nil
}

func (d *dataRepo) GetEvent(context.Context, string) (domain.Event, error) {
	return domain.Event{ID: "e9"}, d.err
}

func (d *dataRepo) ListFacetKeys(context.Context, int64) ([]string, error) {
	return []string{"browser"}, d.err
}

func (d *dataRepo) ListFacetValues(context.Context, int64, string) ([]string, error) {
	return []string{"firefox"}, d.err
}

func (d *dataRepo) SurfaceActivity(context.Context, int64, time.Time) ([]domain.SurfaceActivity, error) {
	return []domain.SurfaceActivity{{}}, d.err
}

func TestReadMethodsReturnRepoData(t *testing.T) {
	t.Parallel()
	svc := New(&dataRepo{}, nil)
	ctx := context.Background()

	if got, err := svc.List(ctx); err != nil || len(got) != 2 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got, err := svc.ListFiltered(ctx, domain.IssueFilter{}); err != nil || len(got) != 2 {
		t.Fatalf("ListFiltered = %v, %v", got, err)
	}
	if got, err := svc.Resolve(ctx, "a"); err != nil || got.Status != "resolved" {
		t.Fatalf("Resolve = %+v, %v", got, err)
	}
	if got, err := svc.HourlyEventCounts(ctx, []int64{1}); err != nil || got[1][3] != 9 {
		t.Fatalf("HourlyEventCounts = %v, %v", got, err)
	}
	if id, err := svc.RowIDByDisplayID(ctx, "BW-1"); err != nil || id != 77 {
		t.Fatalf("RowIDByDisplayID = %d, %v", id, err)
	}
	if evs, more, err := svc.ListEvents(ctx, "a", 10, 0); err != nil || len(evs) != 1 || !more {
		t.Fatalf("ListEvents = %v, %v, %v", evs, more, err)
	}
	if rel, sampled, err := svc.IssueReleases(ctx, "a"); err != nil || len(rel) != 1 || sampled != 12 {
		t.Fatalf("IssueReleases = %v, %d, %v", rel, sampled, err)
	}
	if ev, err := svc.GetEvent(ctx, "e9"); err != nil || ev.ID != "e9" {
		t.Fatalf("GetEvent = %+v, %v", ev, err)
	}
	if keys, err := svc.ListFacetKeys(ctx, 1); err != nil || keys[0] != "browser" {
		t.Fatalf("ListFacetKeys = %v, %v", keys, err)
	}
	if vals, err := svc.ListFacetValues(ctx, 1, "browser"); err != nil || vals[0] != "firefox" {
		t.Fatalf("ListFacetValues = %v, %v", vals, err)
	}
	if act, err := svc.SurfaceActivity(ctx, 1); err != nil || len(act) != 1 {
		t.Fatalf("SurfaceActivity = %v, %v", act, err)
	}
}

func jsonLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

// Each read method that logs must log a server failure at ERROR and keep its
// apperr kind.
func TestReadFailuresKeepKindAndLogByClass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := map[string]func(*Service) error{
		"list":          func(s *Service) error { _, err := s.List(ctx); return err },
		"list filtered": func(s *Service) error { _, err := s.ListFiltered(ctx, domain.IssueFilter{}); return err },
		"hourly":        func(s *Service) error { _, err := s.HourlyEventCounts(ctx, nil); return err },
		"list events":   func(s *Service) error { _, _, err := s.ListEvents(ctx, "a", 1, 0); return err },
		"releases":      func(s *Service) error { _, _, err := s.IssueReleases(ctx, "a"); return err },
		"get event":     func(s *Service) error { _, err := s.GetEvent(ctx, "e"); return err },
		"live events":   func(s *Service) error { _, err := s.ListLiveEvents(ctx, 5, time.Time{}); return err },
		"get":           func(s *Service) error { _, err := s.Get(ctx, "a"); return err },
	}
	for name, call := range calls {
		logger, buf := jsonLogger()
		err := call(New(&dataRepo{fakeRepo{err: apperr.Internal("db", nil)}}, logger))
		if !errors.Is(err, apperr.ErrInternal) {
			t.Errorf("%s: err = %v, want internal", name, err)
		}
		if !strings.Contains(buf.String(), `"level":"ERROR"`) {
			t.Errorf("%s: server failure not logged at error level", name)
		}
	}
}

func TestContextErrorsAreNotLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	calls := map[string]func(*Service) error{
		"list":        func(s *Service) error { _, err := s.List(ctx); return err },
		"filtered":    func(s *Service) error { _, err := s.ListFiltered(ctx, domain.IssueFilter{}); return err },
		"events":      func(s *Service) error { _, _, err := s.ListEvents(ctx, "a", 1, 0); return err },
		"releases":    func(s *Service) error { _, _, err := s.IssueReleases(ctx, "a"); return err },
		"get event":   func(s *Service) error { _, err := s.GetEvent(ctx, "e"); return err },
		"live events": func(s *Service) error { _, err := s.ListLiveEvents(ctx, 5, time.Time{}); return err },
	}
	for name, call := range calls {
		logger, buf := jsonLogger()
		if err := call(New(&dataRepo{fakeRepo{err: context.Canceled}}, logger)); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v", name, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: cancellation logged: %s", name, buf.String())
		}
	}
}

// GetEvent, like Get, treats a missing record as a caller error. HourlyEventCounts
// has no such filter, so it logs everything.
func TestNotFoundHandling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger, buf := jsonLogger()
	svc := New(&dataRepo{fakeRepo{err: apperr.NotFound("gone", nil)}}, logger)
	if _, err := svc.GetEvent(ctx, "e"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("GetEvent err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("missing event logged: %s", buf.String())
	}
}

func TestPassThroughFacetAndSurfaceErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	boom := apperr.Conflict("c", nil)
	svc := New(&dataRepo{fakeRepo{err: boom}}, nil)
	if _, err := svc.RowIDByDisplayID(ctx, "BW-1"); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("RowIDByDisplayID err = %v", err)
	}
	if _, err := svc.ListFacetKeys(ctx, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("ListFacetKeys err = %v", err)
	}
	if _, err := svc.ListFacetValues(ctx, 1, "k"); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("ListFacetValues err = %v", err)
	}
	if _, err := svc.SurfaceActivity(ctx, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("SurfaceActivity err = %v", err)
	}
}
