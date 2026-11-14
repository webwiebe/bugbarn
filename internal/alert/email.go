package alert

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"text/template"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func (d *Deliverer) fireEmail(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
	ev := issue.RepresentativeEvent
	data := alertMailData{
		AlertName:       rule.Name,
		AdminRule:       rule.isAdmin(),
		Origin:          d.env,
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

	subject := alertSubject(d.env, rule, issue)
	release, err := d.acquireMailSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	return d.sendEmail(ctx, d.mailCfg, rule.EmailTo, subject, plain.String(), htmlBuf.String())
}

type alertMailData struct {
	AlertName string
	// AdminRule is true for the synthetic global admin rule, whose AlertName is
	// an internal label rather than something a user chose. The heading omits it.
	AdminRule       bool
	Origin          string
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
	e.Origin = html.EscapeString(d.Origin)
	e.Condition = html.EscapeString(d.Condition)
	e.Severity = html.EscapeString(d.Severity)
	e.Title = html.EscapeString(d.Title)
	e.Project = html.EscapeString(d.Project)
	e.Message = html.EscapeString(d.Message)
	e.Location = html.EscapeString(d.Location)
	e.Environment = html.EscapeString(d.Environment)
	e.Release = html.EscapeString(d.Release)
	// IssueURL is a server-built absolute URL (scheme + host + issue ID); leave as-is.
	// Numeric (EventCount, RegressionCount), server-formatted (FirstSeen, LastSeen)
	// and Sparkline fields are not attacker-influenced free text and stay as-is.
	return e
}

func conditionLabel(condition string) string {
	switch condition {
	case "new_issue":
		return "New issue created"
	case "regression":
		return "Issue regressed"
	case "event_count_exceeds":
		return "Event count exceeded"
	case "event_spike":
		return "Error spike"
	case "silence":
		return "Project silent"
	case "new_issue_flood":
		return "New issues capped"
	case "message_contains":
		return "Message contains match"
	default:
		return condition
	}
}

var alertPlainTmpl = template.Must(template.New("alert-plain").Parse(
	`{{if .Project}}{{.Project}}{{else}}Alert{{end}}{{if and .AlertName (not .AdminRule)}} — {{.AlertName}}{{end}}

Environment: {{if .Origin}}{{.Origin}}{{else}}(unset){{end}}
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
<h2 style="color:#1a1a1a;margin-bottom:4px">{{if .Project}}{{.Project}}{{else}}Alert{{end}}{{if and
  .AlertName (not .AdminRule)}} — {{.AlertName}}{{end}}</h2>
<p style="color:#555;margin-top:0;font-size:13px">{{.Condition}}{{if .Origin}}
  <span style="display:inline-block;margin-left:6px;padding:2px 8px;background:#eef2ff;color:#3538cd;
  border-radius:10px;font-size:11px;font-weight:bold;text-transform:uppercase">{{.Origin}}</span>{{end}}</p>
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
