package ingesthealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/digest"
)

// Notifier delivers an ingest-health alert over a channel that does not touch
// the ingest pipeline. Every implementation here must reach its destination
// without the write queue, the consumer, or the SQLite store — that
// independence is the whole point: the alert says ingest is broken, so it
// cannot be carried by ingest.
type Notifier interface {
	// Name identifies the channel in logs.
	Name() string
	// Notify delivers the alert for an unhealthy snapshot.
	Notify(ctx context.Context, env string, snap Snapshot) error
}

// alertPayload is the JSON body posted by WebhookNotifier.
type alertPayload struct {
	Alert               string    `json:"alert"`
	Environment         string    `json:"environment,omitempty"`
	Reasons             []string  `json:"reasons,omitempty"`
	SampledAt           time.Time `json:"sampledAt"`
	LastEventAt         time.Time `json:"lastEventAt,omitempty"`
	LastEventAgeSeconds float64   `json:"lastEventAgeSeconds"`
	QueueDepth          int64     `json:"queueDepth,omitempty"`
	WALSizeBytes        int64     `json:"walSizeBytes,omitempty"`
}

// WebhookNotifier POSTs the snapshot as JSON. It is the cheapest out-of-band
// channel: one HTTP call to a destination we do not run.
type WebhookNotifier struct {
	url    string
	client *http.Client
}

// NewWebhookNotifier builds a notifier posting to url. A blank url yields nil,
// so callers can wire it unconditionally.
func NewWebhookNotifier(url string) *WebhookNotifier {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return &WebhookNotifier{
		url:    strings.TrimSpace(url),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Name identifies the channel in logs.
func (n *WebhookNotifier) Name() string { return "webhook" }

// Notify posts the alert. Any non-2xx response is an error, so a misconfigured
// endpoint surfaces in the logs rather than failing silently.
func (n *WebhookNotifier) Notify(ctx context.Context, env string, snap Snapshot) error {
	body, err := json.Marshal(alertPayload{
		Alert:               "ingest pipeline unhealthy",
		Environment:         env,
		Reasons:             snap.Reasons,
		SampledAt:           snap.SampledAt,
		LastEventAt:         snap.LastEventAt,
		LastEventAgeSeconds: snap.LastEventAgeSeconds,
		QueueDepth:          snap.QueueDepth,
		WALSizeBytes:        snap.WALSizeBytes,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// EmailNotifier sends the alert over SMTP, reusing the digest mailer. SMTP is a
// separate process on a separate host, so it survives the pipeline being wedged.
type EmailNotifier struct {
	mail digest.MailConfig
	to   string
}

// NewEmailNotifier builds a notifier mailing to. It yields nil when SMTP is not
// configured or no recipient is set, so callers can wire it unconditionally.
func NewEmailNotifier(mail digest.MailConfig, to string) *EmailNotifier {
	to = strings.TrimSpace(to)
	if to == "" || !mail.Enabled || mail.Host == "" {
		return nil
	}
	return &EmailNotifier{mail: mail, to: to}
}

// Name identifies the channel in logs.
func (n *EmailNotifier) Name() string { return "email" }

// Notify sends the alert email.
func (n *EmailNotifier) Notify(ctx context.Context, env string, snap Snapshot) error {
	subject := "BugBarn: ingest pipeline unhealthy"
	if env != "" {
		subject = fmt.Sprintf("BugBarn [%s]: ingest pipeline unhealthy", env)
	}
	return digest.DeliverEmail(ctx, n.mail, n.to, subject, plainBody(env, snap), htmlBody(env, snap))
}

// alertLines are the facts an operator needs to confirm and triage a stall,
// shared by both the plain-text and HTML bodies.
func alertLines(env string, snap Snapshot) []string {
	lines := []string{}
	if env != "" {
		lines = append(lines, "Environment: "+env)
	}
	lines = append(lines,
		"Sampled at: "+snap.SampledAt.Format(time.RFC3339),
		fmt.Sprintf("Last event age: %s", time.Duration(snap.LastEventAgeSeconds)*time.Second),
	)
	if !snap.LastEventAt.IsZero() {
		lines = append(lines, "Last event at: "+snap.LastEventAt.Format(time.RFC3339))
	}
	if snap.QueueDepthKnown {
		lines = append(lines, fmt.Sprintf("Write-queue depth: %d", snap.QueueDepth))
	}
	if snap.WALSizeBytes > 0 {
		lines = append(lines, fmt.Sprintf("WAL size: %d bytes", snap.WALSizeBytes))
	}
	for _, r := range snap.Reasons {
		lines = append(lines, "Reason: "+r)
	}
	return lines
}

func plainBody(env string, snap Snapshot) string {
	var b strings.Builder
	b.WriteString("BugBarn's ingest pipeline is unhealthy.\n\n")
	for _, l := range alertLines(env, snap) {
		b.WriteString(l + "\n")
	}
	b.WriteString("\nThis alert was sent out-of-band: it does not travel through the write queue,\n")
	b.WriteString("so it arrives even when ingest is fully stalled. Events captured during the\n")
	b.WriteString("stall are queued, not lost.\n")
	return b.String()
}

func htmlBody(env string, snap Snapshot) string {
	var b strings.Builder
	b.WriteString("<p><strong>BugBarn's ingest pipeline is unhealthy.</strong></p><ul>")
	for _, l := range alertLines(env, snap) {
		b.WriteString("<li>" + html.EscapeString(l) + "</li>")
	}
	b.WriteString("</ul><p>This alert was sent out-of-band: it does not travel through the write " +
		"queue, so it arrives even when ingest is fully stalled. Events captured during the " +
		"stall are queued, not lost.</p>")
	return b.String()
}
