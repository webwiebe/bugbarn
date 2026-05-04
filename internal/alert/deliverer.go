package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Deliverer sends alert webhook payloads with retry logic.
type Deliverer struct {
	client *http.Client
}

// NewDeliverer creates a new Deliverer with a 5-second HTTP timeout.
func NewDeliverer() *Deliverer {
	return &Deliverer{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Fire sends an alert webhook for the given rule and issue. It retries up to 3 times
// with exponential backoff (1s, 2s, 4s). Returns nil on first success, or the last error.
func (d *Deliverer) Fire(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
	payload, err := d.buildPayload(rule, issue, publicURL)
	if err != nil {
		return fmt.Errorf("build payload: %w", err)
	}

	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.WebhookURL, bytes.NewReader(payload))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return lastErr
}

func (d *Deliverer) buildPayload(rule Rule, issue domain.Issue, publicURL string) ([]byte, error) {
	issueURL := strings.TrimRight(publicURL, "/") + "/issues/" + issue.ID

	switch {
	case strings.Contains(rule.WebhookURL, "hooks.slack.com"):
		return d.slackPayload(rule, issue, issueURL)
	case strings.Contains(rule.WebhookURL, "discord.com/api/webhooks"):
		return d.discordPayload(rule, issue, issueURL)
	default:
		return d.genericPayload(rule, issue, issueURL)
	}
}

func (d *Deliverer) genericPayload(rule Rule, issue domain.Issue, issueURL string) ([]byte, error) {
	payload := map[string]any{
		"alert":     rule.Name,
		"condition": rule.Condition,
		"project":   fmt.Sprintf("%d", rule.ProjectID),
		"issue": map[string]any{
			"id":         issue.ID,
			"title":      issue.Title,
			"url":        issueURL,
			"first_seen": issue.FirstSeen.Format(time.RFC3339),
			"event_count": issue.EventCount,
			"severity":   severityFromIssue(issue),
		},
	}
	return json.Marshal(payload)
}

func (d *Deliverer) slackPayload(rule Rule, issue domain.Issue, issueURL string) ([]byte, error) {
	severity := severityFromIssue(issue)
	text := fmt.Sprintf("[BugBarn] %s: %s", rule.Name, issue.Title)
	payload := map[string]any{
		"text": text,
		"blocks": []any{
			map[string]any{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*[BugBarn]*\n`%s`", issue.Title),
				},
			},
			map[string]any{
				"type": "section",
				"fields": []any{
					map[string]any{
						"type": "mrkdwn",
						"text": fmt.Sprintf("*Severity*\n%s", severity),
					},
					map[string]any{
						"type": "mrkdwn",
						"text": fmt.Sprintf("*Events*\n%d", issue.EventCount),
					},
				},
			},
			map[string]any{
				"type": "actions",
				"elements": []any{
					map[string]any{
						"type": "button",
						"text": map[string]any{
							"type": "plain_text",
							"text": "View Issue",
						},
						"url": issueURL,
					},
				},
			},
		},
	}
	return json.Marshal(payload)
}

func (d *Deliverer) discordPayload(rule Rule, issue domain.Issue, issueURL string) ([]byte, error) {
	severity := severityFromIssue(issue)
	payload := map[string]any{
		"content": "[BugBarn] New issue",
		"embeds": []any{
			map[string]any{
				"title":       issue.Title,
				"description": issue.ExceptionType,
				"color":       15158332, // red
				"fields": []any{
					map[string]any{
						"name":   "Severity",
						"value":  severity,
						"inline": true,
					},
				},
				"url": issueURL,
			},
		},
	}
	return json.Marshal(payload)
}

func severityFromIssue(issue domain.Issue) string {
	if issue.RepresentativeEvent.Severity != "" {
		return strings.ToLower(issue.RepresentativeEvent.Severity)
	}
	return "error"
}
