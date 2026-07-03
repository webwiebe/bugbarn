package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Deliverer sends alert notifications (webhook or email) with retry logic.
type Deliverer struct {
	client  *http.Client
	mailCfg digest.MailConfig
}

// NewDeliverer creates a new Deliverer with a 5-second HTTP timeout.
func NewDeliverer(mailCfg digest.MailConfig) *Deliverer {
	return &Deliverer{
		client:  &http.Client{Timeout: 5 * time.Second},
		mailCfg: mailCfg,
	}
}

// EmailConfigured reports whether SMTP delivery is set up (enabled with a host),
// independent of any per-message recipient. Used to gate the global admin alert.
func (d *Deliverer) EmailConfigured() bool {
	return d.mailCfg.Enabled && d.mailCfg.Host != ""
}

// Fire sends an alert for the given rule and issue. When EmailTo is set it
// sends an email; otherwise it posts to WebhookURL. Returns nil on first success.
func (d *Deliverer) Fire(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
	if rule.EmailTo != "" {
		return d.fireEmail(rule, issue, publicURL)
	}
	return d.fireWebhook(ctx, rule, issue, publicURL)
}

func (d *Deliverer) fireWebhook(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
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

func (d *Deliverer) fireEmail(rule Rule, issue domain.Issue, publicURL string) error {
	issueURL := strings.TrimRight(publicURL, "/") + "/app/#/issues/" + issue.ID
	data := alertMailData{
		AlertName: rule.Name,
		Condition: conditionLabel(rule.Condition),
		Title:     issue.Title,
		IssueURL:  issueURL,
		FirstSeen: issue.FirstSeen.UTC().Format("2006-01-02 15:04 UTC"),
		Severity:  severityFromIssue(issue),
	}

	var plain, html bytes.Buffer
	if err := alertPlainTmpl.Execute(&plain, data); err != nil {
		return fmt.Errorf("render plain: %w", err)
	}
	if err := alertHTMLTmpl.Execute(&html, data); err != nil {
		return fmt.Errorf("render html: %w", err)
	}

	subject := fmt.Sprintf("[BugBarn] %s: %s", rule.Name, issue.Title)
	return digest.DeliverEmail(d.mailCfg, rule.EmailTo, subject, plain.String(), html.String())
}

type alertMailData struct {
	AlertName string
	Condition string
	Title     string
	IssueURL  string
	FirstSeen string
	Severity  string
}

func conditionLabel(condition string) string {
	switch condition {
	case "new_issue":
		return "New issue created"
	case "regression":
		return "Issue regressed"
	case "event_count_exceeds":
		return "Event count exceeded"
	case "message_contains":
		return "Message contains match"
	default:
		return condition
	}
}

var alertPlainTmpl = template.Must(template.New("alert-plain").Parse(
	`[BugBarn] Alert: {{.AlertName}}

Condition: {{.Condition}}
Severity:  {{.Severity}}
First seen: {{.FirstSeen}}

{{.Title}}

{{if .IssueURL}}View issue: {{.IssueURL}}{{end}}`))

var alertHTMLTmpl = template.Must(template.New("alert-html").Parse(
	`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:0 auto;padding:24px">
<h2 style="color:#1a1a1a;margin-bottom:4px">[BugBarn] {{.AlertName}}</h2>
<p style="color:#555;margin-top:0;font-size:13px">{{.Condition}}</p>
<table style="border-collapse:collapse;width:100%;margin:16px 0;background:#f8f9fa;border-radius:4px">
<tr>
  <td style="padding:12px 16px;font-size:13px;color:#555;width:120px">Severity</td>
  <td style="padding:12px 16px;font-size:13px;font-weight:bold">{{.Severity}}</td>
</tr>
<tr style="border-top:1px solid #eee">
  <td style="padding:12px 16px;font-size:13px;color:#555">First seen</td>
  <td style="padding:12px 16px;font-size:13px">{{.FirstSeen}}</td>
</tr>
<tr style="border-top:1px solid #eee">
  <td style="padding:12px 16px;font-size:13px;color:#555">Issue</td>
  <td style="padding:12px 16px;font-size:13px">{{if .IssueURL}}<a href="{{.IssueURL}}" style="color:#0066cc">{{.Title}}</a>{{else}}{{.Title}}{{end}}</td>
</tr>
</table>
{{if .IssueURL}}<p><a href="{{.IssueURL}}" style="display:inline-block;padding:8px 16px;background:#0066cc;color:#fff;text-decoration:none;border-radius:4px;font-size:13px">View Issue →</a></p>{{end}}
</body>
</html>`))

func (d *Deliverer) buildPayload(rule Rule, issue domain.Issue, publicURL string) ([]byte, error) {
	issueURL := strings.TrimRight(publicURL, "/") + "/app/#/issues/" + issue.ID

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
			"id":          issue.ID,
			"title":       issue.Title,
			"url":         issueURL,
			"first_seen":  issue.FirstSeen.Format(time.RFC3339),
			"event_count": issue.EventCount,
			"severity":    severityFromIssue(issue),
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
