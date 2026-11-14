package alert

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (d *Deliverer) buildPayload(rule Rule, issue domain.Issue, publicURL string) ([]byte, error) {
	url := issueURL(publicURL, issue.ID)

	switch {
	case strings.Contains(rule.WebhookURL, "hooks.slack.com"):
		return d.slackPayload(rule, issue, url)
	case strings.Contains(rule.WebhookURL, "discord.com/api/webhooks"):
		return d.discordPayload(rule, issue, url)
	default:
		return d.genericPayload(rule, issue, url)
	}
}

func (d *Deliverer) genericPayload(rule Rule, issue domain.Issue, issueURL string) ([]byte, error) {
	issueFields := map[string]any{
		"id":          issue.ID,
		"title":       issue.Title,
		"url":         issueURL,
		"first_seen":  issue.FirstSeen.Format(time.RFC3339),
		"event_count": issue.EventCount,
		"severity":    severityFromIssue(issue),
	}
	if issue.FirstRelease != "" {
		issueFields["first_release"] = issue.FirstRelease
	}
	payload := map[string]any{
		"alert":     rule.Name,
		"condition": rule.Condition,
		"project":   fmt.Sprintf("%d", rule.ProjectID),
		"issue":     issueFields,
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
