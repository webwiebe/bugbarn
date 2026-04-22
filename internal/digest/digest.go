// Package digest generates and delivers periodic summaries of project error activity.
package digest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// Config holds the delivery settings for the weekly digest.
type Config struct {
	// Scheduling
	Day  int // 0=Sunday … 6=Saturday (UTC)
	Hour int // 0–23 (UTC)

	// Webhook delivery
	WebhookURL string

	// Email delivery
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	To       string

	// Display
	PublicURL   string
	ProjectSlug string
}

// Enabled reports whether at least one delivery channel is configured.
func (c Config) Enabled() bool {
	return c.WebhookURL != "" || (c.SMTPHost != "" && c.To != "")
}

// Store is the subset of storage.Store used by the digest.
type Store interface {
	WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (storage.DigestData, error)
	DefaultProjectID() int64
}

// payload is the JSON shape posted to the webhook.
type payload struct {
	Type        string         `json:"type"`
	PeriodStart string         `json:"period_start"`
	PeriodEnd   string         `json:"period_end"`
	Project     string         `json:"project"`
	Stats       statsBlock     `json:"stats"`
	TopIssues   []issueBlock   `json:"top_issues"`
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

func buildPayload(cfg Config, data storage.DigestData, since, now time.Time) payload {
	p := payload{
		Type:        "weekly_digest",
		PeriodStart: since.UTC().Format(time.RFC3339),
		PeriodEnd:   now.UTC().Format(time.RFC3339),
		Project:     cfg.ProjectSlug,
		Stats: statsBlock{
			TotalEvents:    data.TotalEvents,
			NewIssues:      data.NewIssues,
			ResolvedIssues: data.ResolvedIssues,
			Regressions:    data.Regressions,
		},
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
		p.TopIssues = append(p.TopIssues, ib)
	}
	if p.TopIssues == nil {
		p.TopIssues = []issueBlock{}
	}
	return p
}

// Send gathers digest data and delivers it to all configured channels.
// Errors from individual channels are logged but do not suppress other channels.
func Send(ctx context.Context, cfg Config, store Store) []error {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -7)

	data, err := store.WeeklyDigest(ctx, store.DefaultProjectID(), since)
	if err != nil {
		return []error{fmt.Errorf("digest gather: %w", err)}
	}

	p := buildPayload(cfg, data, since, now)

	var errs []error

	if cfg.WebhookURL != "" {
		if err := sendWebhook(ctx, cfg.WebhookURL, p); err != nil {
			errs = append(errs, fmt.Errorf("digest webhook: %w", err))
		}
	}

	if cfg.SMTPHost != "" && cfg.To != "" {
		if err := sendEmail(cfg, p, since, now); err != nil {
			errs = append(errs, fmt.Errorf("digest email: %w", err))
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
