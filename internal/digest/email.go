package digest

import (
	"bytes"
	"context"
	"fmt"
	"text/template"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/mail"
)

// mailData is the template data bag for both plain and HTML renders.
type mailData struct {
	Start, End     string
	TotalEvents    int
	NewIssues      int
	ResolvedIssues int
	Regressions    int
	Projects       []ProjectSection
	PublicURL      string
}

var plainTmpl = template.Must(template.New("plain").Parse(`BugBarn weekly digest ({{.Start}} – {{.End}} UTC)

All projects: {{.TotalEvents}} events   {{.NewIssues}} new issues   {{.ResolvedIssues}} resolved   {{.Regressions}} regressions
{{range .Projects}}
── {{.Project}} ──
  {{.Stats.TotalEvents}} events   {{.Stats.NewIssues}} new   {{.Stats.ResolvedIssues}} resolved   {{.Stats.Regressions}} regressions
{{- if .TopIssues}}

  Top issues:
{{range .TopIssues}}  · {{.Title}}  ({{.EventCount}} events, {{.Status}}){{if .URL}} — {{.URL}}{{end}}
{{end}}{{- end}}{{end}}{{- if .PublicURL}}
View all issues: {{.PublicURL}}
{{- end}}`))

var htmlTmpl = template.Must(template.New("html").Parse(`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:640px;margin:0 auto;padding:24px">
<h2 style="color:#1a1a1a">BugBarn weekly digest</h2>
<p style="color:#555;margin-top:0">{{.Start}} – {{.End}} UTC</p>

<table style="border-collapse:collapse;width:100%;margin:16px 0">
<tr>
<td style="padding:12px 16px;background:#f5f5f5;text-align:center">
  <div style="font-size:24px;font-weight:bold">{{.TotalEvents}}</div>
  <div style="color:#555;font-size:12px">events</div>
</td>
<td style="padding:12px 16px;background:#fff3cd;text-align:center">
  <div style="font-size:24px;font-weight:bold">{{.NewIssues}}</div>
  <div style="color:#555;font-size:12px">new issues</div>
</td>
<td style="padding:12px 16px;background:#d4edda;text-align:center">
  <div style="font-size:24px;font-weight:bold">{{.ResolvedIssues}}</div>
  <div style="color:#555;font-size:12px">resolved</div>
</td>
<td style="padding:12px 16px;background:#f8d7da;text-align:center">
  <div style="font-size:24px;font-weight:bold">{{.Regressions}}</div>
  <div style="color:#555;font-size:12px">regressions</div>
</td>
</tr>
</table>

{{range .Projects}}
<h3 style="color:#1a1a1a;margin-top:28px;margin-bottom:4px;border-bottom:2px solid #eee;padding-bottom:6px">{{.Project}}</h3>
<p style="color:#555;font-size:13px;margin:4px 0 8px">
  {{.Stats.TotalEvents}} events &nbsp;·&nbsp; {{.Stats.NewIssues}} new &nbsp;·&nbsp;
  {{.Stats.ResolvedIssues}} resolved &nbsp;·&nbsp; {{.Stats.Regressions}} regressions
</p>
{{- if .TopIssues}}
<table style="border-collapse:collapse;width:100%;margin-bottom:8px">
<thead><tr style="background:#f5f5f5">
  <th style="padding:6px 10px;text-align:left;font-size:12px;color:#555">Issue</th>
  <th style="padding:6px 10px;text-align:right;font-size:12px;color:#555">Events</th>
  <th style="padding:6px 10px;text-align:left;font-size:12px;color:#555">Status</th>
</tr></thead>
<tbody>
{{range .TopIssues}}<tr style="border-top:1px solid #eee">
  <td style="padding:6px 10px;font-size:13px">
  {{- if .URL}}<a href="{{.URL}}" style="color:#0066cc">{{.Title}}</a>{{else}}{{.Title}}{{end -}}
  </td>
  <td style="padding:6px 10px;text-align:right;font-size:13px">{{.EventCount}}</td>
  <td style="padding:6px 10px;color:#555;font-size:13px">{{.Status}}</td>
</tr>
{{end}}</tbody>
</table>
{{- end}}
{{end}}
{{- if .PublicURL}}
<p style="margin-top:24px"><a href="{{.PublicURL}}" style="color:#0066cc">View all issues →</a></p>
{{- end}}
</body>
</html>`))

// EmailNotifier delivers the digest via SMTP.
type EmailNotifier struct {
	Cfg mail.Config
}

func (n *EmailNotifier) Name() string { return "email" }

func (n *EmailNotifier) Send(ctx context.Context, report Report) error {
	if !n.Cfg.Active() {
		return nil
	}
	subject, plain, html, err := renderMail(report)
	if err != nil {
		return err
	}
	return mail.Send(ctx, n.Cfg, n.Cfg.To, subject, plain, html)
}

// renderMail builds the subject and both bodies for a report.
func renderMail(report Report) (subject, plain, html string, err error) {
	since, _ := time.Parse(time.RFC3339, report.PeriodStart)
	end, _ := time.Parse(time.RFC3339, report.PeriodEnd)

	d := mailData{
		Start:     since.Format("Jan 2"),
		End:       end.Format("Jan 2 2006"),
		Projects:  report.Projects,
		PublicURL: report.PublicURL,
	}
	for _, sec := range report.Projects {
		d.TotalEvents += sec.Stats.TotalEvents
		d.NewIssues += sec.Stats.NewIssues
		d.ResolvedIssues += sec.Stats.ResolvedIssues
		d.Regressions += sec.Stats.Regressions
	}

	subject = fmt.Sprintf("BugBarn weekly digest: %s - %s", since.Format("Jan 2"), end.Format("Jan 2, 2006"))

	var plainBuf, htmlBuf bytes.Buffer
	if err := plainTmpl.Execute(&plainBuf, d); err != nil {
		return "", "", "", fmt.Errorf("render plain: %w", err)
	}
	if err := htmlTmpl.Execute(&htmlBuf, d); err != nil {
		return "", "", "", fmt.Errorf("render html: %w", err)
	}
	return subject, plainBuf.String(), htmlBuf.String(), nil
}
