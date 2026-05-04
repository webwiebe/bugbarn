package releases

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	releases map[string]domain.Release
	err      error
}

func (f *fakeRepo) ListReleases(context.Context) ([]domain.Release, error) {
	return nil, f.err
}

func (f *fakeRepo) GetRelease(_ context.Context, id string) (domain.Release, error) {
	if f.err != nil {
		return domain.Release{}, f.err
	}
	r, ok := f.releases[id]
	if !ok {
		return domain.Release{}, apperr.NotFound("release not found", nil)
	}
	return r, nil
}

func (f *fakeRepo) CreateRelease(_ context.Context, r domain.Release) (domain.Release, error) {
	if f.err != nil {
		return domain.Release{}, f.err
	}
	r.ID = "rel-1"
	f.releases[r.ID] = r
	return r, nil
}

func (f *fakeRepo) UpdateRelease(_ context.Context, id string, r domain.Release) (domain.Release, error) {
	return domain.Release{}, f.err
}

func (f *fakeRepo) DeleteRelease(_ context.Context, id string) error {
	return f.err
}

func (f *fakeRepo) UploadSourceMap(_ context.Context, _ domain.SourceMapUpload) (domain.SourceMap, error) {
	return domain.SourceMap{}, f.err
}

func (f *fakeRepo) ListSourceMaps(context.Context) ([]domain.SourceMapMeta, error) {
	return nil, f.err
}

func TestCreate_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{releases: map[string]domain.Release{}}
	svc := New(repo, nil)

	input := domain.Release{Name: "v1.0.0", Version: "1.0.0"}
	got, err := svc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID == "" {
		t.Error("expected non-empty ID")
	}
	if got.Name != "v1.0.0" {
		t.Errorf("Name: got %q, want %q", got.Name, "v1.0.0")
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{releases: map[string]domain.Release{}}
	svc := New(repo, nil)

	_, err := svc.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
