package service

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Repository interface {
	ListIssues(context.Context) ([]storage.Issue, error)
	GetIssue(context.Context, string) (storage.Issue, error)
	ListIssueEvents(context.Context, string) ([]storage.Event, error)
	GetEvent(context.Context, string) (storage.Event, error)
	ListRecentEvents(context.Context, int, time.Time) ([]storage.Event, error)
	ResolveIssue(context.Context, string) (storage.Issue, error)
	ReopenIssue(context.Context, string) (storage.Issue, error)
	ListReleases(context.Context) ([]storage.Release, error)
	GetRelease(context.Context, string) (storage.Release, error)
	CreateRelease(context.Context, storage.Release) (storage.Release, error)
	UpdateRelease(context.Context, string, storage.Release) (storage.Release, error)
	DeleteRelease(context.Context, string) error
	ListAlerts(context.Context) ([]storage.Alert, error)
	GetAlert(context.Context, string) (storage.Alert, error)
	CreateAlert(context.Context, storage.Alert) (storage.Alert, error)
	UpdateAlert(context.Context, string, storage.Alert) (storage.Alert, error)
	DeleteAlert(context.Context, string) error
	GetSettings(context.Context) (map[string]string, error)
	UpdateSettings(context.Context, map[string]string) error
	UploadSourceMap(context.Context, storage.SourceMapUpload) (storage.SourceMap, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ListIssues(ctx context.Context) ([]storage.Issue, error) {
	return s.repo.ListIssues(ctx)
}

func (s *Service) GetIssue(ctx context.Context, id string) (storage.Issue, error) {
	return s.repo.GetIssue(ctx, id)
}

func (s *Service) ListIssueEvents(ctx context.Context, issueID string) ([]storage.Event, error) {
	return s.repo.ListIssueEvents(ctx, issueID)
}

func (s *Service) GetEvent(ctx context.Context, id string) (storage.Event, error) {
	return s.repo.GetEvent(ctx, id)
}

func (s *Service) ListLiveEvents(ctx context.Context, limit int, since time.Time) ([]storage.Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-15 * time.Minute)
	}
	return s.repo.ListRecentEvents(ctx, limit, since)
}

func (s *Service) ResolveIssue(ctx context.Context, id string) (storage.Issue, error) {
	return s.repo.ResolveIssue(ctx, id)
}

func (s *Service) ReopenIssue(ctx context.Context, id string) (storage.Issue, error) {
	return s.repo.ReopenIssue(ctx, id)
}

func (s *Service) ListReleases(ctx context.Context) ([]storage.Release, error) {
	return s.repo.ListReleases(ctx)
}

func (s *Service) GetRelease(ctx context.Context, id string) (storage.Release, error) {
	return s.repo.GetRelease(ctx, id)
}

func (s *Service) CreateRelease(ctx context.Context, release storage.Release) (storage.Release, error) {
	return s.repo.CreateRelease(ctx, release)
}

func (s *Service) UpdateRelease(ctx context.Context, id string, release storage.Release) (storage.Release, error) {
	return s.repo.UpdateRelease(ctx, id, release)
}

func (s *Service) DeleteRelease(ctx context.Context, id string) error {
	return s.repo.DeleteRelease(ctx, id)
}

func (s *Service) ListAlerts(ctx context.Context) ([]storage.Alert, error) {
	return s.repo.ListAlerts(ctx)
}

func (s *Service) GetAlert(ctx context.Context, id string) (storage.Alert, error) {
	return s.repo.GetAlert(ctx, id)
}

func (s *Service) CreateAlert(ctx context.Context, alert storage.Alert) (storage.Alert, error) {
	return s.repo.CreateAlert(ctx, alert)
}

func (s *Service) UpdateAlert(ctx context.Context, id string, alert storage.Alert) (storage.Alert, error) {
	return s.repo.UpdateAlert(ctx, id, alert)
}

func (s *Service) DeleteAlert(ctx context.Context, id string) error {
	return s.repo.DeleteAlert(ctx, id)
}

func (s *Service) GetSettings(ctx context.Context) (map[string]string, error) {
	return s.repo.GetSettings(ctx)
}

func (s *Service) UpdateSettings(ctx context.Context, values map[string]string) error {
	return s.repo.UpdateSettings(ctx, values)
}

func (s *Service) UploadSourceMap(ctx context.Context, upload storage.SourceMapUpload) (storage.SourceMap, error) {
	return s.repo.UploadSourceMap(ctx, upload)
}
