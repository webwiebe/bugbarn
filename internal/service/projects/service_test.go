package projects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	projects map[string]domain.Project
	err      error
}

func (f *fakeRepo) ListProjects(context.Context) ([]domain.Project, error) {
	return nil, f.err
}

func (f *fakeRepo) CreateProject(_ context.Context, name, slug string) (domain.Project, error) {
	if f.err != nil {
		return domain.Project{}, f.err
	}
	p := domain.Project{ID: 1, Name: name, Slug: slug, CreatedAt: time.Now()}
	return p, nil
}

func (f *fakeRepo) EnsureProject(_ context.Context, slug string) (domain.Project, error) {
	return domain.Project{}, f.err
}

func (f *fakeRepo) EnsureProjectPending(_ context.Context, slug string) (domain.Project, error) {
	return domain.Project{}, f.err
}

func (f *fakeRepo) ApproveProject(_ context.Context, slug string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.projects[slug]; !ok {
		return apperr.NotFound("project not found", nil)
	}
	return nil
}

func (f *fakeRepo) ProjectBySlug(_ context.Context, slug string) (domain.Project, error) {
	if f.err != nil {
		return domain.Project{}, f.err
	}
	p, ok := f.projects[slug]
	if !ok {
		return domain.Project{}, apperr.NotFound("project not found", nil)
	}
	return p, nil
}

func (f *fakeRepo) DefaultProjectID() int64 { return 1 }

func (f *fakeRepo) DeleteProject(_ context.Context, slug string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.projects[slug]; !ok {
		return apperr.NotFound("project not found", nil)
	}
	delete(f.projects, slug)
	return nil
}

func (f *fakeRepo) ListAPIKeys(context.Context) ([]domain.APIKey, error) {
	return nil, f.err
}

func (f *fakeRepo) CreateAPIKey(_ context.Context, _ string, _ int64, _, _ string) (domain.APIKey, error) {
	return domain.APIKey{}, f.err
}

func (f *fakeRepo) DeleteAPIKey(_ context.Context, _ int64) error {
	return f.err
}

func (f *fakeRepo) EnsureSetupAPIKey(_ context.Context, _ string, _ int64, _ string) error {
	return f.err
}

func (f *fakeRepo) ValidAPIKeySHA256(_ context.Context, _ string) (int64, string, bool, error) {
	return 0, "", false, f.err
}

func (f *fakeRepo) TouchAPIKey(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeRepo) GetSettings(context.Context) (map[string]string, error) {
	return nil, f.err
}

func (f *fakeRepo) UpdateSettings(context.Context, map[string]string) error {
	return f.err
}

func (f *fakeRepo) CreateAlias(_ context.Context, _ string, _ int64) error {
	return f.err
}

func (f *fakeRepo) DeleteAlias(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeRepo) ResolveAlias(_ context.Context, _ string) (int64, error) {
	return 0, apperr.NotFound("not found", nil)
}

func (f *fakeRepo) RenameProject(_ context.Context, _, _, _ string) error {
	return f.err
}

func (f *fakeRepo) MergeProjects(_ context.Context, _, _ string) error {
	return f.err
}

func (f *fakeRepo) CreateGroup(_ context.Context, name, slug string) (domain.ProjectGroup, error) {
	return domain.ProjectGroup{}, f.err
}

func (f *fakeRepo) ListGroups(context.Context) ([]domain.ProjectGroup, error) {
	return nil, f.err
}

func (f *fakeRepo) DeleteGroup(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeRepo) AssignProjectToGroup(_ context.Context, _, _ string) error {
	return f.err
}

func (f *fakeRepo) RemoveProjectFromGroup(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeRepo) ListGroupProjects(_ context.Context, _ string) ([]domain.Project, error) {
	return nil, f.err
}

func TestCreate_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{projects: map[string]domain.Project{}}
	svc := New(repo, nil)

	got, err := svc.Create(context.Background(), "My Project", "my-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Slug != "my-project" {
		t.Errorf("Slug: got %q, want %q", got.Slug, "my-project")
	}
	if got.Name != "My Project" {
		t.Errorf("Name: got %q, want %q", got.Name, "My Project")
	}
}

func TestCreate_Conflict(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{err: apperr.Conflict("slug taken", nil)}
	svc := New(repo, nil)

	_, err := svc.Create(context.Background(), "Dup", "dup")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestBySlug_NotFound(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{projects: map[string]domain.Project{}}
	svc := New(repo, nil)

	_, err := svc.BySlug(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestApprove_NotFound(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{projects: map[string]domain.Project{}}
	svc := New(repo, nil)

	err := svc.Approve(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
