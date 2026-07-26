package digest

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// emailDeliveryCounter counts digest email-send attempts, built once from the
// global meter (see tracing.Meter) rather than recreated per call.
var emailDeliveryCounter metric.Int64Counter

func init() {
	emailDeliveryCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.digest.email_delivery",
		metric.WithDescription("Digest email delivery attempts, by outcome and attempt number."),
		metric.WithUnit("{attempt}"),
	)
}

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

// DeliverEmail sends the message with up to 3 attempts, retrying only on
// transient failures (mirrors rapid-root email.ts retryEmailSend).
func DeliverEmail(ctx context.Context, mc MailConfig, to, subject, plain, html string) error {
	mc.To = to
	return deliverEmail(ctx, mc, subject, plain, html)
}

func deliverEmail(ctx context.Context, mc MailConfig, subject, plain, html string) error {
	from := mc.From
	if from == "" {
		from = mc.User
	}

	boundary := "==BugBarnDigest=="
	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + mc.To + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
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
	// Only authenticate when credentials are actually configured. Building an
	// auth unconditionally meant a relay that does not advertise AUTH was
	// rejected outright ("server doesn't support AUTH") even though we had no
	// credentials to offer it — unauthenticated internal relays could never
	// work. Nothing that authenticates today changes: a configured User still
	// produces exactly the same PlainAuth.
	var auth smtp.Auth
	if mc.User != "" {
		auth = smtp.PlainAuth("", mc.User, mc.Pass, mc.Host)
	}
	raw := []byte(msg.String())

	delays := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}
	var lastErr error
	for attempt, delay := range delays {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		lastErr = sendMailTraced(ctx, mc.Host, attempt+1, func() error {
			// Bound each attempt even when the caller gave us no deadline, so
			// no configuration can leave this blocking forever.
			attemptCtx, cancel := context.WithTimeout(ctx, smtpAttemptTimeout)
			defer cancel()
			return sendMail(attemptCtx, addr, mc.Host, auth, from, []string{mc.To}, raw)
		})
		if lastErr == nil {
			return nil
		}
		if !transientSMTPError(lastErr) {
			return lastErr
		}
		if attempt < len(delays)-1 {
			slog.Warn("digest mailer: transient error, retrying", "attempt", attempt+1, "max_attempts", 3, "retry_in", delay, "error", lastErr)
			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}

// smtpAttemptTimeout bounds one SMTP attempt when the caller's context carries
// no deadline of its own. A var rather than a const so tests can shrink it
// instead of waiting out the real thing.
var smtpAttemptTimeout = 30 * time.Second

// sendMail is a context-aware replacement for smtp.SendMail. The stdlib helper
// dials with no timeout and never consults a context, so a server that accepts
// the TCP connection and then stalls pins the caller forever — for the weekly
// digest that means the scheduler goroutine never returns and every LATER
// digest silently stops firing too.
//
// The protocol flow mirrors smtp.SendMail exactly, including its security
// properties: STARTTLS whenever the server advertises it, and refusing to
// authenticate over an unencrypted link (smtp.PlainAuth.Start enforces that
// itself, so it still applies here).
func sendMail(ctx context.Context, addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	client, err := dialSMTP(ctx, addr, host, auth)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return writeMessage(client, from, to, msg)
}

// dialSMTP opens the connection and completes the greeting, STARTTLS and AUTH
// handshake, all bounded by ctx.
func dialSMTP(ctx context.Context, addr, host string, auth smtp.Auth) (*smtp.Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// net/smtp has no context plumbing, so translate the deadline onto the
	// socket: that is what actually bounds a mid-conversation stall.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			_ = client.Close()
			return nil, errors.New("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	return client, nil
}

// writeMessage plays the envelope and body over an established client.
func writeMessage(client *smtp.Client, from string, to []string, msg []byte) error {
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// sendMailTraced wraps a single SMTP send attempt in a span, recording the
// mail host and attempt number and marking the span as failed on error. The
// existing slog-based retry logging in deliverEmail is unchanged; this adds
// span-level visibility alongside it.
func sendMailTraced(ctx context.Context, host string, attempt int, send func() error) error {
	_, span := tracing.Tracer().Start(ctx, "digest.EmailSend")
	defer span.End()
	span.SetAttributes(
		attribute.String("smtp.host", host),
		attribute.Int("attempt", attempt),
	)
	if err := send(); err != nil {
		span.SetStatus(codes.Error, err.Error())
		emailDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("outcome", "error"),
			attribute.Int("attempt", attempt),
		))
		return err
	}
	emailDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", "success"),
		attribute.Int("attempt", attempt),
	))
	return nil
}

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
  {{.Stats.TotalEvents}} events &nbsp;·&nbsp; {{.Stats.NewIssues}} new &nbsp;·&nbsp; {{.Stats.ResolvedIssues}} resolved &nbsp;·&nbsp; {{.Stats.Regressions}} regressions
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
  <td style="padding:6px 10px;font-size:13px">{{if .URL}}<a href="{{.URL}}" style="color:#0066cc">{{.Title}}</a>{{else}}{{.Title}}{{end}}</td>
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
	Cfg MailConfig
}

func (n *EmailNotifier) Name() string { return "email" }

func (n *EmailNotifier) Send(ctx context.Context, report Report) error {
	if !n.Cfg.active() {
		return nil
	}

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

	subject := fmt.Sprintf("BugBarn weekly digest: %s - %s", since.Format("Jan 2"), end.Format("Jan 2, 2006"))

	var plain, html bytes.Buffer
	if err := plainTmpl.Execute(&plain, d); err != nil {
		return fmt.Errorf("render plain: %w", err)
	}
	if err := htmlTmpl.Execute(&html, d); err != nil {
		return fmt.Errorf("render html: %w", err)
	}

	return deliverEmail(ctx, n.Cfg, subject, plain.String(), html.String())
}
