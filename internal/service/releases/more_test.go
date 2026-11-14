package releases

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

// scriptedRepo returns canned values and a configurable error per call.
type scriptedRepo struct {
	releases []domain.Release
	byID     map[string]domain.Release
	maps     []domain.SourceMapMeta
	err      error
	listErr  error
}

func (r *scriptedRepo) ListReleases(context.Context) ([]domain.Release, error) {
	return r.releases, r.listErr
}
func (r *scriptedRepo) ListRecentReleases(context.Context, int) ([]domain.Release, error) {
	return r.releases, r.err
}
func (r *scriptedRepo) GetRelease(_ context.Context, id string) (domain.Release, error) {
	if r.err != nil {
		return domain.Release{}, r.err
	}
	rel, ok := r.byID[id]
	if !ok {
		return domain.Release{}, apperr.NotFound("release not found", nil)
	}
	return rel, nil
}
func (r *scriptedRepo) CreateRelease(_ context.Context, rel domain.Release) (domain.Release, error) {
	if r.err != nil {
		return domain.Release{}, r.err
	}
	rel.ID = "rel-1"
	return rel, nil
}
func (r *scriptedRepo) UpdateRelease(_ context.Context, id string, rel domain.Release) (domain.Release, error) {
	if r.err != nil {
		return domain.Release{}, r.err
	}
	rel.ID = id
	return rel, nil
}
func (r *scriptedRepo) DeleteRelease(context.Context, string) error { return r.err }
func (r *scriptedRepo) UploadSourceMap(context.Context, domain.SourceMapUpload) (domain.SourceMap, error) {
	if r.err != nil {
		return domain.SourceMap{}, r.err
	}
	return domain.SourceMap{ID: "sm-1"}, nil
}
func (r *scriptedRepo) ListSourceMaps(context.Context) ([]domain.SourceMapMeta, error) {
	return r.maps, r.err
}

func loggerAndBuf() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestUpdateDeleteHappyPath(t *testing.T) {
	logger, buf := loggerAndBuf()
	svc := New(&scriptedRepo{}, logger)
	got, err := svc.Update(context.Background(), "r1", domain.Release{Name: "v2"})
	if err != nil || got.ID != "r1" || got.Name != "v2" {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if err := svc.Delete(context.Background(), "r1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !strings.Contains(buf.String(), "release deleted") {
		t.Fatalf("delete not logged: %s", buf.String())
	}
}

func TestSourceMapUploadAndList(t *testing.T) {
	logger, buf := loggerAndBuf()
	repo := &scriptedRepo{maps: []domain.SourceMapMeta{{}, {}}}
	svc := New(repo, logger)
	sm, err := svc.UploadSourceMap(context.Background(), domain.SourceMapUpload{Release: "v1", BundleURL: "/a.js"})
	if err != nil || sm.ID != "sm-1" {
		t.Fatalf("UploadSourceMap = %+v, %v", sm, err)
	}
	if !strings.Contains(buf.String(), "source map uploaded") {
		t.Fatalf("upload not logged: %s", buf.String())
	}
	maps, err := svc.ListSourceMaps(context.Background())
	if err != nil || len(maps) != 2 {
		t.Fatalf("ListSourceMaps = %v, %v", maps, err)
	}
}

func TestErrorsKeepKindAndLogAtErrorLevel(t *testing.T) {
	cause := apperr.Internal("clash", nil)
	calls := map[string]func(*Service) error{
		"get":    func(s *Service) error { _, err := s.Get(context.Background(), "x"); return err },
		"create": func(s *Service) error { _, err := s.Create(context.Background(), domain.Release{}); return err },
		"update": func(s *Service) error { _, err := s.Update(context.Background(), "x", domain.Release{}); return err },
		"delete": func(s *Service) error { return s.Delete(context.Background(), "x") },
		"upload": func(s *Service) error {
			_, err := s.UploadSourceMap(context.Background(), domain.SourceMapUpload{})
			return err
		},
	}
	for name, call := range calls {
		logger, buf := loggerAndBuf()
		err := call(New(&scriptedRepo{err: cause}, logger))
		if !errors.Is(err, apperr.ErrInternal) {
			t.Errorf("%s: err = %v, want internal", name, err)
		}
		if !strings.Contains(buf.String(), `"level":"ERROR"`) {
			t.Errorf("%s: no error log: %s", name, buf.String())
		}
	}
}

// List and ListSourceMaps log an internal failure once, like the other methods.
func TestListInternalErrorsAreLogged(t *testing.T) {
	logger, buf := loggerAndBuf()
	svc := New(&scriptedRepo{err: apperr.Internal("db", nil)}, logger)
	if _, err := svc.List(context.Background()); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("List err = %v", err)
	}
	if _, err := svc.ListSourceMaps(context.Background()); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("ListSourceMaps err = %v", err)
	}
	if n := strings.Count(buf.String(), `"level":"ERROR"`); n != 2 {
		t.Fatalf("ERROR records = %d, want 2: %s", n, buf.String())
	}
}

func TestCreateSkipsLoggingForContextErrors(t *testing.T) {
	logger, buf := loggerAndBuf()
	svc := New(&scriptedRepo{err: context.Canceled}, logger)
	if _, err := svc.Create(context.Background(), domain.Release{Name: "v1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("cancellation was logged: %s", buf.String())
	}
}

func TestNewIssueWindow(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rel := func(id, env string, days int) domain.Release {
		return domain.Release{ID: id, Environment: env, ObservedAt: t0.AddDate(0, 0, days)}
	}
	all := []domain.Release{
		rel("a", "prod", 0), rel("b", "prod", 10), rel("c", "prod", 5),
		rel("d", "staging", 2), rel("e", "prod", -3),
	}
	byID := map[string]domain.Release{}
	for _, r := range all {
		byID[r.ID] = r
	}
	svc := New(&scriptedRepo{releases: all, byID: byID}, nil)
	ctx := context.Background()

	from, before, err := svc.NewIssueWindow(ctx, "a")
	if err != nil || !from.Equal(t0) || !before.Equal(t0.AddDate(0, 0, 5)) {
		t.Fatalf("window a = %v..%v, %v; next prod release is day 5", from, before, err)
	}
	if _, before, _ := svc.NewIssueWindow(ctx, "b"); !before.IsZero() {
		t.Fatalf("latest release should leave the window open, got %v", before)
	}
	if _, before, _ := svc.NewIssueWindow(ctx, "d"); !before.IsZero() {
		t.Fatalf("staging release is not ended by prod releases, got %v", before)
	}
}

func TestNewIssueWindowErrors(t *testing.T) {
	ctx := context.Background()
	if _, _, err := New(&scriptedRepo{byID: map[string]domain.Release{}}, nil).NewIssueWindow(ctx, "x"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("missing release err = %v", err)
	}
	repo := &scriptedRepo{byID: map[string]domain.Release{"x": {ID: "x"}}, listErr: apperr.Internal("db", nil)}
	if _, _, err := New(repo, nil).NewIssueWindow(ctx, "x"); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("list failure err = %v", err)
	}
}
