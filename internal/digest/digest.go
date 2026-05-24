// Package digest generates and delivers periodic summaries of project error activity.
package digest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Config controls when and how the digest is delivered.
type Config struct {
	Day        int // 0=Sunday … 6=Saturday
	Hour       int // 0–23
	WebhookURL string
	Mail       MailConfig
	PublicURL  string
}

func (c Config) Enabled() bool {
	return c.WebhookURL != "" || c.Mail.active()
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
			ib.URL = fmt.Sprintf("%s/#/issues/%s", cfg.PublicURL, iss.ID)
		}
		sec.TopIssues = append(sec.TopIssues, ib)
	}
	return sec
}

// Send gathers digest data across all projects and delivers to all configured
// notifiers. Projects with no activity in the period are silently skipped.
// Each notifier is attempted independently; failures are returned but do not
// suppress others.
func Send(ctx context.Context, cfg Config, store Store, notifiers []Notifier) []error {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)

	projects, err := store.ListProjects(ctx)
	if err != nil {
		return []error{fmt.Errorf("list projects: %w", err)}
	}

	report := Report{
		PeriodStart: since.UTC().Format(time.RFC3339),
		PeriodEnd:   now.UTC().Format(time.RFC3339),
		PublicURL:   cfg.PublicURL,
	}

	var errs []error
	for _, proj := range projects {
		data, err := store.WeeklyDigest(ctx, proj.ID, since)
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

	if len(report.Projects) == 0 {
		slog.Info("digest: no activity across all projects, skipping")
		return errs
	}

	for _, n := range notifiers {
		if err := n.Send(ctx, report); err != nil {
			slog.Error("digest: delivery failed", "channel", n.Name(), "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
		}
	}

	return errs
}
