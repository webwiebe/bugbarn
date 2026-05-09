package digest

import "context"

// Report is the channel-agnostic digest representation passed to every Notifier.
type Report struct {
	PeriodStart string           `json:"period_start"`
	PeriodEnd   string           `json:"period_end"`
	PublicURL   string           `json:"public_url,omitempty"`
	Projects    []ProjectSection `json:"projects"`
}

// Notifier delivers a digest report through a specific channel (email, webhook, Slack, …).
type Notifier interface {
	Send(ctx context.Context, report Report) error
	Name() string
}
