package alert

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/mail"
)

// EventVolumeSource provides recent per-issue event volume for sparklines.
// Index 0 of the returned array is the oldest hour, index 23 the current hour.
type EventVolumeSource interface {
	HourlyEventCounts(ctx context.Context, issueID string) ([24]int, error)
}

// Deliverer sends alert notifications (webhook or email) with retry logic.
type Deliverer struct {
	client  *http.Client
	mailCfg mail.Config
	vol     EventVolumeSource
	env     string // this BugBarn instance's environment (production/staging/testing), labels emails

	// Email sends are bounded (see mail_limit.go): mailSlots caps concurrent
	// SMTP sends, mailQueue caps how many more may wait.
	sendEmail    func(ctx context.Context, mc mail.Config, to, subject, plain, html string) error
	mailSlots    chan struct{}
	mailQueue    int64
	emailWaiting atomic.Int64
	emailDropped atomic.Int64
}

// NewDeliverer creates a new Deliverer with a 5-second HTTP timeout.
func NewDeliverer(mailCfg mail.Config) *Deliverer {
	d := &Deliverer{
		client:    &http.Client{Timeout: 5 * time.Second},
		mailCfg:   mailCfg,
		sendEmail: mail.Send,
	}
	d.setMailLimits(defaultMailConcurrency, defaultMailQueue)
	return d
}

// SetEventVolumeSource wires an optional source used to render the 24h event
// sparkline on regression emails. When nil, the sparkline is omitted.
func (d *Deliverer) SetEventVolumeSource(src EventVolumeSource) {
	d.vol = src
}

// SetEnvironment records which BugBarn instance (production/staging/testing) is
// sending, so alert emails identify their origin. Empty leaves emails unlabeled.
func (d *Deliverer) SetEnvironment(env string) {
	d.env = strings.TrimSpace(env)
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
