package digest

import (
	"bytes"
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"text/template"
	"time"
)

// MailConfig holds SMTP settings. All fields are optional: when Enabled is
// false (or Host/To are empty) the mailer is a no-op. Env vars follow the
// same naming convention as rapid-root: SMTP_HOST, SMTP_PORT, SMTP_USER,
// SMTP_PASS, SMTP_FROM — with BUGBARN_DIGEST_ENABLED and BUGBARN_DIGEST_TO
// as the BugBarn-specific opt-in controls.
type MailConfig struct {
	Enabled bool
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	To      string
}

// active reports whether SMTP delivery is configured and enabled.
func (m MailConfig) active() bool {
	return m.Enabled && m.Host != "" && m.To != ""
}

// transientSMTPError returns true for network-level errors that are safe to
// retry. Auth failures and permanent rejections return false so we fail fast.
func transientSMTPError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"ETIMEDOUT", "ECONNREFUSED", "ENOTFOUND", "connection reset", "broken pipe", "socket", "i/o timeout"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// deliverEmail sends the message with up to 3 attempts, retrying only on
// transient failures (mirrors rapid-root email.ts retryEmailSend).
func deliverEmail(mc MailConfig, subject, plain, html string) error {
	from := mc.From
	if from == "" {
		from = mc.User
	}

	boundary := "==BugBarnDigest=="
	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + mc.To + "\r\n")
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n\r\n")
	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	msg.WriteString(plain + "\r\n")
	msg.WriteString("--" + boundary + "\r\n")
	msg.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	msg.WriteString(html + "\r\n")
	msg.WriteString("--" + boundary + "--\r\n")

	addr := fmt.Sprintf("%s:%d", mc.Host, mc.Port)
	auth := smtp.PlainAuth("", mc.User, mc.Pass, mc.Host)
	raw := []byte(msg.String())

	delays := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}
	var lastErr error
	for attempt, delay := range delays {
		lastErr = smtp.SendMail(addr, auth, from, []string{mc.To}, raw)
		if lastErr == nil {
			return nil
		}
		if !transientSMTPError(lastErr) {
			return lastErr
		}
		if attempt < len(delays)-1 {
			log.Printf("digest mailer: transient error (attempt %d/3), retrying in %s: %v", attempt+1, delay, lastErr)
			time.Sleep(delay)
		}
	}
	return lastErr
}

// mailData is the template data bag for both plain and HTML renders.
type mailData struct {
	Project        string
	Start, End     string
	TotalEvents    int
	NewIssues      int
	ResolvedIssues int
	Regressions    int
	TopIssues      []issueBlock
	PublicURL      string
}

var plainTmpl = template.Must(template.New("plain").Parse(`This week in {{.Project}} ({{.Start}} – {{.End}} UTC):

  {{.TotalEvents}} events   {{.NewIssues}} new issues   {{.ResolvedIssues}} resolved   {{.Regressions}} regressions
{{- if .TopIssues}}

Top issues by volume:
{{range .TopIssues}}  #{{.ID}}  {{.Title}}  ({{.EventCount}} events, {{.Status}})
{{end}}{{- end}}{{- if .PublicURL}}
View all issues: {{.PublicURL}}
{{- end}}`))

var htmlTmpl = template.Must(template.New("html").Parse(`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:0 auto;padding:24px">
<h2 style="color:#1a1a1a">BugBarn weekly digest — {{.Project}}</h2>
<p style="color:#555">{{.Start}} – {{.End}} UTC</p>
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
{{- if .TopIssues}}
<h3 style="color:#1a1a1a;margin-top:24px">Top issues by volume</h3>
<table style="border-collapse:collapse;width:100%">
<thead><tr style="background:#f5f5f5">
  <th style="padding:8px 12px;text-align:left;font-size:12px;color:#555">Issue</th>
  <th style="padding:8px 12px;text-align:right;font-size:12px;color:#555">Events</th>
  <th style="padding:8px 12px;text-align:left;font-size:12px;color:#555">Status</th>
</tr></thead>
<tbody>
{{range .TopIssues}}<tr style="border-top:1px solid #eee">
  <td style="padding:8px 12px">{{if .URL}}<a href="{{.URL}}" style="color:#0066cc">{{.Title}}</a>{{else}}{{.Title}}{{end}}</td>
  <td style="padding:8px 12px;text-align:right">{{.EventCount}}</td>
  <td style="padding:8px 12px;color:#555">{{.Status}}</td>
</tr>
{{end}}</tbody>
</table>
{{- end}}
{{- if .PublicURL}}
<p style="margin-top:24px"><a href="{{.PublicURL}}" style="color:#0066cc">View all issues →</a></p>
{{- end}}
</body>
</html>`))

func sendEmailDigest(mc MailConfig, p payload, since, now time.Time) error {
	if !mc.active() {
		return nil
	}

	d := mailData{
		Project:        p.Project,
		Start:          since.Format("Jan 2"),
		End:            now.Format("Jan 2 2006"),
		TotalEvents:    p.Stats.TotalEvents,
		NewIssues:      p.Stats.NewIssues,
		ResolvedIssues: p.Stats.ResolvedIssues,
		Regressions:    p.Stats.Regressions,
		TopIssues:      p.TopIssues,
		PublicURL:      p.PublicURL,
	}

	subject := fmt.Sprintf("[BugBarn] Weekly digest — %s — %s–%s",
		p.Project, since.Format("Jan 2"), now.Format("Jan 2 2006"))

	var plain, html bytes.Buffer
	if err := plainTmpl.Execute(&plain, d); err != nil {
		return fmt.Errorf("render plain: %w", err)
	}
	if err := htmlTmpl.Execute(&html, d); err != nil {
		return fmt.Errorf("render html: %w", err)
	}

	return deliverEmail(mc, subject, plain.String(), html.String())
}
