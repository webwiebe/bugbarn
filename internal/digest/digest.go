// Package digest generates and delivers periodic summaries of project error activity.
package digest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Budgets bounding a digest run. Gathering is per-project rather than one
// deadline for the whole fleet: a single slow project must not consume the
// budget of every project after it, and must not eat into delivery.
const (
	// DefaultGatherBudget bounds gathering across all projects.
	DefaultGatherBudget = 10 * time.Minute
	// DefaultProjectBudget bounds one project's stats queries.
	DefaultProjectBudget = 30 * time.Second
	// deliveryBudget bounds delivery to all notifiers. It is derived from the
	// caller's context, not the gather budget, so a slow or partially failed
	// gather still ships the report it did manage to build.
	deliveryBudget = 2 * time.Minute
)

// Config controls when and how the digest is delivered.
type Config struct {
	Day        int // 0=Sunday … 6=Saturday
	Hour       int // 0–23
	WebhookURL string
	Mail       MailConfig
	PublicURL  string
	// GatherBudget bounds the data-gathering phase. Zero means DefaultGatherBudget.
	GatherBudget time.Duration
	// ProjectBudget bounds one project's gather. Zero means DefaultProjectBudget.
	ProjectBudget time.Duration
}

func (c Config) Enabled() bool {
	return c.WebhookURL != "" || c.Mail.active()
}

func (c Config) gatherBudget() time.Duration {
	if c.GatherBudget > 0 {
		return c.GatherBudget
	}
	return DefaultGatherBudget
}

func (c Config) projectBudget() time.Duration {
	if c.ProjectBudget > 0 {
		return c.ProjectBudget
	}
	return DefaultProjectBudget
}

// Store is the subset of storage needed by the digest.
type Store interface {
	WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (domain.DigestData, error)
	ListProjects(ctx context.Context) ([]domain.Project, error)
}

type ProjectSection struct {
	Project   string       `json:"project"`
	Stats     StatsBlock   `json:"stats"`
	TopIssues []IssueBlock `json:"top_issues"`
}

type StatsBlock struct {
	TotalEvents    int `json:"total_events"`
	NewIssues      int `json:"new_issues"`
	ResolvedIssues int `json:"resolved_issues"`
	Regressions    int `json:"regressions"`
}

type IssueBlock struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	EventCount int    `json:"event_count"`
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
}

func buildSection(cfg Config, slug string, data domain.DigestData) ProjectSection {
	sec := ProjectSection{
		Project: slug,
		Stats: StatsBlock{
			TotalEvents:    data.TotalEvents,
			NewIssues:      data.NewIssues,
			ResolvedIssues: data.ResolvedIssues,
			Regressions:    data.Regressions,
		},
		TopIssues: []IssueBlock{},
	}
	for _, iss := range data.TopIssues {
		ib := IssueBlock{
			ID:         iss.ID,
			Title:      iss.Title,
			EventCount: iss.EventCount,
			Status:     iss.Status,
		}
		if cfg.PublicURL != "" {
			ib.URL = fmt.Sprintf("%s/app/#/issues/%s", cfg.PublicURL, iss.ID)
		}
		sec.TopIssues = append(sec.TopIssues, ib)
	}
	return sec
}

// Send gathers digest data across all projects and delivers to all configured
// notifiers. Projects with no activity in the period are silently skipped.
// Each notifier is attempted independently; failures are returned but do not
// suppress others.
//
// Gathering runs under its own budget (cfg.gatherBudget()), with each project
// further bounded by projectBudget. Delivery is budgeted separately from the
// caller's context, so whatever was gathered still ships.
func Send(ctx context.Context, cfg Config, store Store, notifiers []Notifier) []error {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)

	gatherCtx, cancelGather := context.WithTimeout(ctx, cfg.gatherBudget())
	defer cancelGather()

	projects, err := store.ListProjects(gatherCtx)
	if err != nil {
		return []error{fmt.Errorf("list projects: %w", err)}
	}

	report := Report{
		PeriodStart: since.UTC().Format(time.RFC3339),
		PeriodEnd:   now.UTC().Format(time.RFC3339),
		PublicURL:   cfg.PublicURL,
	}

	errs := gather(gatherCtx, cfg, store, projects, since, &report)

	if len(report.Projects) == 0 {
		slog.Info("digest: no activity across all projects, skipping")
		return errs
	}

	deliverCtx, cancelDeliver := context.WithTimeout(ctx, deliveryBudget)
	defer cancelDeliver()

	for _, n := range notifiers {
		if err := n.Send(deliverCtx, report); err != nil {
			slog.Error("digest: delivery failed", "channel", n.Name(), "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
		}
	}

	return errs
}

// gather collects per-project stats into report, one project at a time under
// its own deadline. If the overall gather budget runs out it stops and reports
// that once, rather than emitting an identical deadline error per remaining
// project.
func gather(
	ctx context.Context, cfg Config, store Store,
	projects []domain.Project, since time.Time, report *Report,
) []error {
	var errs []error
	for i, proj := range projects {
		if err := ctx.Err(); err != nil {
			slog.Error("digest: gather budget exhausted",
				"gathered", i, "total", len(projects), "error", err)
			errs = append(errs, fmt.Errorf(
				"gather budget exhausted after %d/%d projects: %w", i, len(projects), err))
			break
		}

		projCtx, cancel := context.WithTimeout(ctx, cfg.projectBudget())
		data, err := store.WeeklyDigest(projCtx, proj.ID, since)
		cancel()
		if err != nil {
			slog.Error("digest: failed to gather project data", "project", proj.Slug, "error", err)
			errs = append(errs, fmt.Errorf("gather %s: %w", proj.Slug, err))
			continue
		}
		if data.TotalEvents == 0 && data.NewIssues == 0 && data.ResolvedIssues == 0 && data.Regressions == 0 {
			continue
		}
		report.Projects = append(report.Projects, buildSection(cfg, proj.Slug, data))
	}
	return errs
}
