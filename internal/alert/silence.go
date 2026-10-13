package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// SilenceNotifier mails the admin address when a project that used to report
// has gone quiet. It satisfies silence.Notifier.
type SilenceNotifier struct {
	deliverer notifier
	adminTo   string
	publicURL string
}

// NewSilenceNotifier builds a notifier sending to adminTo through d.
func NewSilenceNotifier(d *Deliverer, adminTo, publicURL string) *SilenceNotifier {
	return &SilenceNotifier{deliverer: d, adminTo: adminTo, publicURL: publicURL}
}

// NotifySilent sends one mail about slug having reported nothing for silentFor.
func (n *SilenceNotifier) NotifySilent(ctx context.Context, slug string, silentFor time.Duration) error {
	days := int(silentFor.Hours() / 24)
	rule := Rule{
		ID:        AdminRuleIDPrefix + "silence",
		Name:      "Admin notifications",
		Enabled:   true,
		EmailTo:   n.adminTo,
		Condition: "silence",
	}
	issue := domain.Issue{
		Title:       fmt.Sprintf("No errors from this project for %d days; it reported regularly before", days),
		ProjectSlug: slug,
	}
	return n.deliverer.Fire(ctx, rule, issue, n.publicURL)
}
