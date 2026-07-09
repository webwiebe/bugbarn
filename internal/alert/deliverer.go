package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// EventVolumeSource provides recent per-issue event volume for sparklines.
// Index 0 of the returned array is the oldest hour, index 23 the current hour.
type EventVolumeSource interface {
	HourlyEventCounts(ctx context.Context, issueID string) ([24]int, error)
}

// Deliverer sends alert notifications (webhook or email) with retry logic.
type Deliverer struct {
	client  *http.Client
	mailCfg digest.MailConfig
	vol     EventVolumeSource
}

// NewDeliverer creates a new Deliverer with a 5-second HTTP timeout.
func NewDeliverer(mailCfg digest.MailConfig) *Deliverer {
	return &Deliverer{
		client:  &http.Client{Timeout: 5 * time.Second},
		mailCfg: mailCfg,
	}
}

// SetEventVolumeSource wires an optional source used to render the 24h event
// sparkline on regression emails. When nil, the sparkline is omitted.
func (d *Deliverer) SetEventVolumeSource(src EventVolumeSource) {
	d.vol = src
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
		return d.fireEmail(ctx, rule, issue, publicURL)
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

func (d *Deliverer) fireEmail(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
	ev := issue.RepresentativeEvent
	data := alertMailData{
		AlertName:       rule.Name,
		Condition:       conditionLabel(rule.Condition),
		Title:           issue.Title,
		IssueURL:        issueURL(publicURL, issue.ID),
		Severity:        severityFromIssue(issue),
		Project:         issue.ProjectSlug,
		Message:         messageOf(ev),
		Location:        locationOf(ev),
		Environment:     firstAttr(ev, "environment", "deployment.environment", "env"),
		Release:         firstAttr(ev, "release", "service.version", "version"),
		EventCount:      issue.EventCount,
		FirstSeen:       formatWhen(issue.FirstSeen),
		LastSeen:        formatWhen(issue.LastSeen),
		Regression:      rule.Condition == "regression",
		RegressionCount: issue.RegressionCount,
	}
	if data.Regression && d.vol != nil {
		if counts, err := d.vol.HourlyEventCounts(ctx, issue.ID); err == nil {
			data.Sparkline = buildSparkline(counts)
		}
	}

	var plain, htmlBuf bytes.Buffer
	if err := alertPlainTmpl.Execute(&plain, data); err != nil {
		return fmt.Errorf("render plain: %w", err)
	}
	if err := alertHTMLTmpl.Execute(&htmlBuf, data.escaped()); err != nil {
		return fmt.Errorf("render html: %w", err)
	}

	subject := fmt.Sprintf("[BugBarn] %s: %s", rule.Name, issue.Title)
	return digest.DeliverEmail(d.mailCfg, rule.EmailTo, subject, plain.String(), htmlBuf.String())
}

type alertMailData struct {
	AlertName       string
	Condition       string
	Title           string
	IssueURL        string
	Severity        string
	Project         string
	Message         string
	Location        string
	Environment     string
	Release         string
	EventCount      int
	FirstSeen       string
	LastSeen        string
	Regression      bool
	RegressionCount int
	Sparkline       []sparkBar
}

// escaped returns a copy with all free-text string fields HTML-escaped, for
// safe interpolation into the (text/template-based) HTML email. Numeric and
// sparkline fields are inherently safe and left untouched. The plain-text
// template renders from the unescaped original.
func (d alertMailData) escaped() alertMailData {
	e := d
	e.AlertName = html.EscapeString(d.AlertName)
	e.Title = html.EscapeString(d.Title)
	e.Project = html.EscapeString(d.Project)
	e.Message = html.EscapeString(d.Message)
	e.Location = html.EscapeString(d.Location)
	e.Environment = html.EscapeString(d.Environment)
	e.Release = html.EscapeString(d.Release)
	// IssueURL is a server-built absolute URL (scheme + host + issue ID); leave as-is.
	return e
}

// sparkBar is a single hourly bar in the 24h event-volume sparkline.
type sparkBar struct {
	Height int  // pixel height, 2..40
	Count  int  // events in this hour
	Zero   bool // true when Count == 0 (rendered faint)
}

// issueURL builds the absolute dashboard URL for an issue. It returns "" when
// no public URL is configured (so callers omit the link rather than emit a
// relative href that mail clients mis-resolve, e.g. to x-webdoc://). A bare
// host without a scheme is upgraded to https://.
func issueURL(publicURL, issueID string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return base + "/app/#/issues/" + issueID
}

// buildSparkline scales a 24h hourly count array to fixed-height bars.
func buildSparkline(counts [24]int) []sparkBar {
	max := 0
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	bars := make([]sparkBar, len(counts))
	for i, c := range counts {
		b := sparkBar{Count: c, Zero: c == 0}
		switch {
		case c == 0:
			b.Height = 2
		case max <= 0:
			b.Height = 2
		default:
			b.Height = 4 + (c*36)/max // 4..40px
		}
		bars[i] = b
	}
	return bars
}

// messageOf returns the most descriptive error message available on an event.
func messageOf(ev event.Event) string {
	if m := strings.TrimSpace(ev.Exception.Message); m != "" {
		return m
	}
	return strings.TrimSpace(ev.Message)
}

// locationOf returns a "function (file:line)" culprit from the top stack frame.
func locationOf(ev event.Event) string {
	for _, f := range ev.Exception.Stacktrace {
		fn := strings.TrimSpace(f.Function)
		file := strings.TrimSpace(f.File)
		if fn == "" && file == "" {
			continue
		}
		switch {
		case file == "":
			return fn
		case f.Line > 0:
			loc := fmt.Sprintf("%s:%d", file, f.Line)
			if fn != "" {
				return fn + " (" + loc + ")"
			}
			return loc
		default:
			if fn != "" {
				return fn + " (" + file + ")"
			}
			return file
		}
	}
	return ""
}

// firstAttr returns the first non-empty value found across an event's
// attributes and resource maps for any of the candidate keys.
func firstAttr(ev event.Event, keys ...string) string {
	for _, m := range []map[string]any{ev.Attributes, ev.Resource} {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" && s != "<nil>" {
					return s
				}
			}
		}
	}
	return ""
}

// formatWhen renders a timestamp for display, or "" if it is the zero value.
func formatWhen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
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

Condition:  {{.Condition}}
Severity:   {{.Severity}}
{{if .Project}}Project:    {{.Project}}
{{end}}{{if .Regression}}Regressed:  {{.RegressionCount}} time(s)
{{end}}Events:     {{.EventCount}}
First seen: {{.FirstSeen}}
{{if .LastSeen}}Last seen:  {{.LastSeen}}
{{end}}
{{.Title}}
{{if .Message}}
{{.Message}}
{{end}}{{if .Location}}
at {{.Location}}
{{end}}{{if or .Environment .Release}}
{{if .Environment}}Environment: {{.Environment}}  {{end}}{{if .Release}}Release: {{.Release}}{{end}}
{{end}}{{if .IssueURL}}
View issue: {{.IssueURL}}{{end}}`))

var alertHTMLTmpl = template.Must(template.New("alert-html").Parse(
	`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:0 auto;padding:24px">
<h2 style="color:#1a1a1a;margin-bottom:4px">[BugBarn] {{.AlertName}}</h2>
<p style="color:#555;margin-top:0;font-size:13px">{{.Condition}}</p>
<table style="border-collapse:collapse;width:100%;margin:16px 0;background:#f8f9fa;border-radius:4px">
<tr>
  <td style="padding:10px 16px;font-size:13px;color:#555;width:120px">Severity</td>
  <td style="padding:10px 16px;font-size:13px;font-weight:bold">{{.Severity}}</td>
</tr>
{{if .Project}}<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Project</td>
  <td style="padding:10px 16px;font-size:13px">{{.Project}}</td>
</tr>{{end}}
{{if .Regression}}<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Regressed</td>
  <td style="padding:10px 16px;font-size:13px">{{.RegressionCount}} time(s)</td>
</tr>{{end}}
<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Events</td>
  <td style="padding:10px 16px;font-size:13px">{{.EventCount}}</td>
</tr>
<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">First seen</td>
  <td style="padding:10px 16px;font-size:13px">{{.FirstSeen}}</td>
</tr>
{{if .LastSeen}}<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Last seen</td>
  <td style="padding:10px 16px;font-size:13px">{{.LastSeen}}</td>
</tr>{{end}}
{{if .Environment}}<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Environment</td>
  <td style="padding:10px 16px;font-size:13px">{{.Environment}}</td>
</tr>{{end}}
{{if .Release}}<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Release</td>
  <td style="padding:10px 16px;font-size:13px">{{.Release}}</td>
</tr>{{end}}
<tr style="border-top:1px solid #eee">
  <td style="padding:10px 16px;font-size:13px;color:#555">Issue</td>
  <td style="padding:10px 16px;font-size:13px">{{if .IssueURL}}<a href="{{.IssueURL}}"
    style="color:#0066cc">{{.Title}}</a>{{else}}{{.Title}}{{end}}</td>
</tr>
</table>
{{if .Message}}<div
  style="margin:16px 0;padding:12px 16px;background:#fff5f5;border-left:3px solid #d33;border-radius:4px">
<div
  style="font-family:ui-monospace,Menlo,monospace;font-size:13px;color:#a00;word-break:break-word">{{.Message}}</div>
{{if .Location}}<div
  style="font-family:ui-monospace,Menlo,monospace;font-size:12px;color:#888;margin-top:6px">at {{.Location}}</div>{{end}}
</div>{{end}}
{{if .Sparkline}}<div style="margin:16px 0">
<div style="font-size:12px;color:#555;margin-bottom:6px">Events over the last 24 hours</div>
<table role="presentation" cellpadding="0" cellspacing="0" style="border-collapse:collapse;height:44px"><tr>
{{range .Sparkline}}<td style="vertical-align:bottom;padding:0 1px"><div
    style="width:7px;height:{{.Height}}px;background:{{if .Zero}}#e0e0e0{{else}}#0066cc{{end}};border-radius:1px"
    title="{{.Count}} events"></div></td>{{end}}
</tr></table>
</div>{{end}}
{{if .IssueURL}}<p><a href="{{.IssueURL}}"
  style="display:inline-block;padding:8px 16px;background:#0066cc;color:#fff;text-decoration:none;
  border-radius:4px;font-size:13px">View Issue →</a></p>{{end}}
</body>
</html>`))

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
