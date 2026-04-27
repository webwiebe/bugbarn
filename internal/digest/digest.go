// Package digest generates and delivers periodic summaries of project error activity.
package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Config controls when and how the digest is delivered.
// Toggle email via Mail.Enabled; toggle webhook by setting/clearing WebhookURL.
// Neither channel starting means no scheduler goroutine is launched.
type Config struct {
	// Scheduling (UTC)
	Day  int // 0=Sunday … 6=Saturday
	Hour int // 0–23

	// Webhook delivery — set to "" to disable
	WebhookURL string

	// Email delivery — set Mail.Enabled=false to disable without losing credentials
	Mail MailConfig

	// Display
	PublicURL string
}

// Enabled reports whether at least one delivery channel is active.
func (c Config) Enabled() bool {
	return c.WebhookURL != "" || c.Mail.active()
}

// Store is the subset of storage.Store used by the digest.
type Store interface {
	WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (storage.DigestData, error)
	ListProjects(ctx context.Context) ([]storage.Project, error)
}

// payload is the JSON shape POSTed to the webhook.
type payload struct {
	Type        string           `json:"type"`
	PeriodStart string           `json:"period_start"`
	PeriodEnd   string           `json:"period_end"`
	PublicURL   string           `json:"public_url,omitempty"`
	Projects    []projectSection `json:"projects"`
}

type projectSection struct {
	Project   string       `json:"project"`
	Stats     statsBlock   `json:"stats"`
	TopIssues []issueBlock `json:"top_issues"`
}

type statsBlock struct {
	TotalEvents    int `json:"total_events"`
	NewIssues      int `json:"new_issues"`
	ResolvedIssues int `json:"resolved_issues"`
	Regressions    int `json:"regressions"`
}

type issueBlock struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	EventCount int    `json:"event_count"`
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
}

func buildSection(cfg Config, slug string, data storage.DigestData) projectSection {
	sec := projectSection{
		Project: slug,
		Stats: statsBlock{
			TotalEvents:    data.TotalEvents,
			NewIssues:      data.NewIssues,
			ResolvedIssues: data.ResolvedIssues,
			Regressions:    data.Regressions,
		},
		TopIssues: []issueBlock{},
	}
	for _, iss := range data.TopIssues {
		ib := issueBlock{
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
// channels. Projects with no activity in the period are silently skipped.
// If no projects have any activity the send is a no-op. Each channel is
// attempted independently; failures are returned but do not suppress others.
func Send(ctx context.Context, cfg Config, store Store) []error {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)

	projects, err := store.ListProjects(ctx)
	if err != nil {
		return []error{fmt.Errorf("list projects: %w", err)}
	}

	p := payload{
		Type:        "weekly_digest",
		PeriodStart: since.UTC().Format(time.RFC3339),
		PeriodEnd:   now.UTC().Format(time.RFC3339),
		PublicURL:   cfg.PublicURL,
	}

	for _, proj := range projects {
		data, err := store.WeeklyDigest(ctx, proj.ID, since)
		if err != nil {
			return []error{fmt.Errorf("gather %s: %w", proj.Slug, err)}
		}
		if data.TotalEvents == 0 {
			continue
		}
		p.Projects = append(p.Projects, buildSection(cfg, proj.Slug, data))
	}

	if len(p.Projects) == 0 {
		log.Printf("digest: no activity across all projects, skipping")
		return nil
	}

	var errs []error

	if cfg.WebhookURL != "" {
		if err := sendWebhook(ctx, cfg.WebhookURL, p); err != nil {
			log.Printf("digest webhook: %v", err)
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}

	if cfg.Mail.active() {
		if err := sendEmailDigest(cfg.Mail, p, since, now); err != nil {
			log.Printf("digest email: %v", err)
			errs = append(errs, fmt.Errorf("email: %w", err))
		}
	}

	return errs
}

func sendWebhook(ctx context.Context, url string, p payload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
