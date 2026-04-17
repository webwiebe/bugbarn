package alert

import "time"

// Rule represents an alert rule that fires webhooks when conditions are met.
type Rule struct {
	ID              string
	Name            string
	Enabled         bool
	ProjectID       int64
	WebhookURL      string
	Condition       string // "new_issue" | "regression" | "event_count_exceeds"
	Threshold       int
	CooldownMinutes int
	LastFiredAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Firing records a single alert delivery for an issue.
type Firing struct {
	ID      int64
	AlertID string
	IssueID string
	FiredAt time.Time
}
