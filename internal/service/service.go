package service

import (
	"context"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type Repository interface {
	ListIssues(context.Context) ([]storage.Issue, error)
	ListIssuesFiltered(context.Context, storage.IssueFilter) ([]storage.Issue, error)
	GetIssue(context.Context, string) (storage.Issue, error)
	ListIssueEvents(context.Context, string, int, int64) ([]storage.Event, bool, error)
	GetEvent(context.Context, string) (storage.Event, error)
	ListRecentEvents(context.Context, int, time.Time) ([]storage.Event, error)
	ResolveIssue(context.Context, string) (storage.Issue, error)
	ReopenIssue(context.Context, string) (storage.Issue, error)
	MuteIssue(context.Context, string, string) (storage.Issue, error)
	UnmuteIssue(context.Context, string) (storage.Issue, error)
	HourlyEventCounts(context.Context, []int64) (map[int64][24]int, error)
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
	ListSourceMaps(context.Context) ([]storage.SourceMapMeta, error)
	ListFacetKeys(context.Context, int64) ([]string, error)
	ListFacetValues(context.Context, int64, string) ([]string, error)
}

type Service struct {
	repo Repository
	bus  *domainevents.Bus
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// NewWithBus creates a Service that publishes domain events on the given bus.
func NewWithBus(repo Repository, bus *domainevents.Bus) *Service {
	return &Service{repo: repo, bus: bus}
}

// PublishIssueEvent publishes IssueCreated or IssueRegressed domain events
// when a new event is persisted. Safe to call when bus is nil.
func (s *Service) PublishIssueEvent(issue storage.Issue, projectID int64, isNew bool, isRegressed bool) {
	if s.bus == nil {
		return
	}
	if isNew {
		s.bus.Publish(domainevents.IssueCreated{Issue: issue, ProjectID: projectID})
	}
	if isRegressed {
		s.bus.Publish(domainevents.IssueRegressed{Issue: issue, ProjectID: projectID})
	}
}

func (s *Service) ListIssues(ctx context.Context) ([]storage.Issue, error) {
	return s.repo.ListIssues(ctx)
}

func (s *Service) ListIssuesFiltered(ctx context.Context, filter storage.IssueFilter) ([]storage.Issue, error) {
	return s.repo.ListIssuesFiltered(ctx, filter)
}

func (s *Service) GetIssue(ctx context.Context, id string) (storage.Issue, error) {
	return s.repo.GetIssue(ctx, id)
}

func (s *Service) ListIssueEvents(ctx context.Context, issueID string, limit int, beforeID int64) ([]storage.Event, bool, error) {
	return s.repo.ListIssueEvents(ctx, issueID, limit, beforeID)
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

func (s *Service) MuteIssue(ctx context.Context, id, muteMode string) (storage.Issue, error) {
	return s.repo.MuteIssue(ctx, id, muteMode)
}

func (s *Service) UnmuteIssue(ctx context.Context, id string) (storage.Issue, error) {
	return s.repo.UnmuteIssue(ctx, id)
}

func (s *Service) HourlyEventCounts(ctx context.Context, issueIDs []int64) (map[int64][24]int, error) {
	return s.repo.HourlyEventCounts(ctx, issueIDs)
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

func (s *Service) ListSourceMaps(ctx context.Context) ([]storage.SourceMapMeta, error) {
	return s.repo.ListSourceMaps(ctx)
}

func (s *Service) ListFacetKeys(ctx context.Context, projectID int64) ([]string, error) {
	return s.repo.ListFacetKeys(ctx, projectID)
}

func (s *Service) ListFacetValues(ctx context.Context, projectID int64, key string) ([]string, error) {
	return s.repo.ListFacetValues(ctx, projectID, key)
}
