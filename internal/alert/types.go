package alert

import (
	"strings"
	"time"
)

// AdminRuleIDPrefix marks the synthetic rules built by notifyAdmin for the
// global admin recipient, as opposed to rules a user created for a project.
// Their Name is an internal label ("Admin notifications"), not something the
// user chose, so it is not worth space in an email subject.
const AdminRuleIDPrefix = "admin-"

// Rule represents an alert rule that fires webhooks when conditions are met.
type Rule struct {
	ID              string
	Name            string
	Enabled         bool
	ProjectID       int64
	WebhookURL      string
	EmailTo         string
	Condition       string // "new_issue" | "regression" | "event_count_exceeds" | "message_contains"
	Param           string // condition-specific string parameter (e.g. substring for message_contains)
	Threshold       int
	CooldownMinutes int
	LastFiredAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// isAdmin reports whether this is a synthetic global admin rule rather than one
// a user configured for a project.
func (r Rule) isAdmin() bool {
	return strings.HasPrefix(r.ID, AdminRuleIDPrefix)
}

// Firing records a single alert delivery for an issue.
type Firing struct {
	ID      int64
	AlertID string
	IssueID string
	FiredAt time.Time
}
