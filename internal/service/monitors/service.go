// Package monitors accepts check-ins from scheduled jobs and workers. A monitor
// that stays silent past its interval and grace is found by the miss detector;
// this package validates the input and records the latest signal.
package monitors

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const (
	// MinInterval and MaxInterval bound how often a monitor can be expected.
	MinInterval = time.Minute
	MaxInterval = 31 * 24 * time.Hour
	// MaxMessageLen caps the free text of a check-in.
	MaxMessageLen = 200
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Repository is the storage the service needs; *storage.MonitorStore satisfies it.
type Repository interface {
	RecordMonitorCheckIn(ctx context.Context, in storage.MonitorCheckIn) (storage.MonitorCheckInResult, error)
	GetMonitor(ctx context.Context, projectID int64, slug string) (domain.Monitor, error)
	ListMonitors(ctx context.Context, projectID int64) ([]domain.Monitor, error)
	MarkMonitorEmitted(ctx context.Context, id int64, now time.Time) error
	ListEnabledMonitors(ctx context.Context) ([]domain.Monitor, error)
}

// Reporter turns incidents into issues; *monitorwatch.Reporter satisfies it.
type Reporter interface {
	Errored(ctx context.Context, m domain.Monitor, message string) error
	Recovered(ctx context.Context, m domain.Monitor, at time.Time) error
}

// Service validates and records check-ins.
type Service struct {
	repo     Repository
	reporter Reporter
	logger   *slog.Logger
	now      func() time.Time
}

// New builds the service over repo.
func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "monitors"), now: time.Now}
}

// SetReporter wires the issue reporting. Without one, check-ins are recorded
// and nothing opens or resolves an issue.
func (s *Service) SetReporter(r Reporter) { s.reporter = r }

// SetClock replaces the time source. Tests use it to fix the check-in time.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// CheckInInput is one check-in. Interval and Grace are optional: sent, they
// create or update the monitor; left out, the stored values stay.
type CheckInInput struct {
	Slug     string
	Status   string
	Message  string
	Interval *time.Duration
	Grace    *time.Duration
}

// CheckInResult is the monitor after the check-in. Message is the cleaned
// text, kept for the event the miss detector emits; it is not stored.
type CheckInResult struct {
	Monitor       domain.Monitor
	Created       bool
	PreviousState string
	Message       string
}

// CheckIn validates the input and records it for a project.
func (s *Service) CheckIn(ctx context.Context, projectID int64, in CheckInInput) (CheckInResult, error) {
	if !slugPattern.MatchString(in.Slug) {
		return CheckInResult{}, apperr.InvalidInput("monitor slug must be 1-64 characters of a-z, 0-9, '.', '_' or '-'", nil)
	}
	if in.Status != domain.CheckInOK && in.Status != domain.CheckInError {
		return CheckInResult{}, apperr.InvalidInput(`status must be "ok" or "error"`, nil)
	}
	if err := validateTimings(in.Interval, in.Grace); err != nil {
		return CheckInResult{}, err
	}
	res, err := s.repo.RecordMonitorCheckIn(ctx, storage.MonitorCheckIn{
		ProjectID: projectID, Slug: in.Slug, Status: in.Status,
		Interval: in.Interval, Grace: in.Grace, At: s.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, apperr.ErrInternal) {
			s.logger.Error("monitor check-in lost", "project_id", projectID, "monitor", in.Slug, "error", err)
		}
		return CheckInResult{}, err
	}
	out := CheckInResult{
		Monitor: res.Monitor, Created: res.Created, PreviousState: res.PreviousState,
		Message: cleanMessage(in.Message),
	}
	s.report(ctx, out)
	return out, nil
}

// report opens an issue for an error check-in and resolves the open ones when
// a monitor recovers. A failure is logged and does not fail the check-in: the
// job did report in, and an unreported error check-in is not marked as sent.
func (s *Service) report(ctx context.Context, res CheckInResult) {
	if s.reporter == nil {
		return
	}
	m, now := res.Monitor, s.now().UTC()
	switch {
	case m.State == domain.MonitorStateError && m.NeedsReport(now):
		if err := s.reporter.Errored(ctx, m, res.Message); err != nil {
			s.logger.Error("monitor error check-in not reported", "project_id", m.ProjectID, "monitor", m.Slug, "error", err)
			return
		}
		if err := s.repo.MarkMonitorEmitted(ctx, m.ID, now); err != nil {
			s.logger.Error("monitor report not recorded", "project_id", m.ProjectID, "monitor", m.Slug, "error", err)
		}
	case m.State == domain.MonitorStateOK && res.PreviousState != domain.MonitorStateOK:
		if err := s.reporter.Recovered(ctx, m, now); err != nil {
			s.logger.Error("monitor recovery not recorded", "project_id", m.ProjectID, "monitor", m.Slug, "error", err)
		}
	}
}

// ListAll returns the enabled monitors of every active project, with their
// project slug.
func (s *Service) ListAll(ctx context.Context) ([]domain.Monitor, error) {
	return s.repo.ListEnabledMonitors(ctx)
}

// List returns the monitors of a project.
func (s *Service) List(ctx context.Context, projectID int64) ([]domain.Monitor, error) {
	return s.repo.ListMonitors(ctx, projectID)
}

func validateTimings(interval, grace *time.Duration) error {
	if interval != nil && (*interval < MinInterval || *interval > MaxInterval) {
		return apperr.InvalidInput("interval must be between 1m and 31d", nil)
	}
	if grace != nil && *grace < 0 {
		return apperr.InvalidInput("grace must not be negative", nil)
	}
	if interval != nil && grace != nil && *grace > *interval {
		return apperr.InvalidInput("grace must not exceed the interval", nil)
	}
	return nil
}

// cleanMessage trims the text and cuts it at MaxMessageLen characters.
func cleanMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if utf8.RuneCountInString(msg) <= MaxMessageLen {
		return msg
	}
	return string([]rune(msg)[:MaxMessageLen])
}
