package domain

import (
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/cronexpr"
	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// Issue represents a grouped error occurrence.
type Issue struct {
	ID                     string      `json:"id"`
	IssueNumber            int         `json:"issue_number"`
	Fingerprint            string      `json:"fingerprint"`
	FingerprintMaterial    string      `json:"fingerprint_material"`
	FingerprintExplanation []string    `json:"fingerprint_explanation"`
	Title                  string      `json:"title"`
	NormalizedTitle        string      `json:"normalized_title"`
	ExceptionType          string      `json:"exception_type,omitempty"`
	Status                 string      `json:"status"`
	MuteMode               string      `json:"mute_mode,omitempty"`
	ResolvedAt             time.Time   `json:"resolved_at"`
	ReopenedAt             time.Time   `json:"reopened_at"`
	LastRegressedAt        time.Time   `json:"last_regressed_at"`
	RegressionCount        int         `json:"regression_count"`
	FirstSeen              time.Time   `json:"first_seen"`
	LastSeen               time.Time   `json:"last_seen"`
	EventCount             int         `json:"event_count"`
	RepresentativeEvent    event.Event `json:"representative_event"`
	ProjectSlug            string      `json:"project_slug,omitempty"`
	ProjectID              int64       `json:"project_id,omitempty"`

	// SampleRate is how many occurrences one stored event of this issue stands
	// for: 1 when everything is stored, 100 when the issue is loud enough that
	// only every hundredth event is kept. EventCount is unaffected either way —
	// it always counts every occurrence.
	SampleRate int `json:"sample_rate,omitempty"`
}

// IssueFilter holds optional filters and sort order for listing issues.
type IssueFilter struct {
	Sort   string
	Status string
	Query  string
	Facets map[string]string
	Limit  int
	Offset int

	// FirstSeenFrom and FirstSeenBefore bound issues.first_seen to the half-open
	// range [FirstSeenFrom, FirstSeenBefore). A zero value leaves that side open.
	FirstSeenFrom   time.Time
	FirstSeenBefore time.Time
}

// IssueCounts is the number of issues per status within one scope (a project, a
// project group, or everything). Open covers unresolved and regressed issues,
// the same set the "open" list filter returns.
type IssueCounts struct {
	Open      int `json:"open"`
	Regressed int `json:"regressed"`
	Resolved  int `json:"resolved"`
	Muted     int `json:"muted"`
	All       int `json:"all"`
}

// For returns the count that matches a list status filter. An empty or unknown
// filter means all issues.
func (c IssueCounts) For(status string) int {
	switch status {
	case "open":
		return c.Open
	case "regressed":
		return c.Regressed
	case "resolved":
		return c.Resolved
	case "muted":
		return c.Muted
	}
	return c.All
}

// Event represents a single captured error event.
type Event struct {
	ID                     string      `json:"id"`
	IssueID                string      `json:"issue_id"`
	Fingerprint            string      `json:"fingerprint"`
	FingerprintMaterial    string      `json:"fingerprint_material"`
	FingerprintExplanation []string    `json:"fingerprint_explanation"`
	ReceivedAt             time.Time   `json:"received_at"`
	ObservedAt             time.Time   `json:"observed_at"`
	Severity               string      `json:"severity"`
	Message                string      `json:"message"`
	Regressed              bool        `json:"regressed"`
	Payload                event.Event `json:"payload"`
}

// Project represents a project row.
type Project struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Status       string    `json:"status"`
	IssuePrefix  string    `json:"issue_prefix"`
	IssueCounter int       `json:"issue_counter"`
	GroupID      *int64    `json:"group_id"`
	CreatedAt    time.Time `json:"created_at"`

	// RetentionDays overrides how long this project's events are kept. nil means
	// it inherits the deployment-wide window; a value may only be shorter than
	// that window, since the global sweep would delete anything older anyway.
	RetentionDays *int `json:"retention_days"`
	// SamplingMode is "" (inherit the deployment default), "on" or "off".
	SamplingMode string `json:"sampling_mode"`
}

// ProjectGroup represents a named collection of related projects.
type ProjectGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// ProjectAlias is a slug that transparently redirects to another project.
type ProjectAlias struct {
	AliasSlug   string `json:"alias_slug"`
	ProjectID   int64  `json:"project_id"`
	ProjectSlug string `json:"project_slug"`
}

// Scope constants for API keys.
const (
	APIKeyScopeFull   = "full"
	APIKeyScopeIngest = "ingest"
	APIKeyScopeRead   = "read"
)

// APIKey represents an API key row (the plaintext key is never stored).
type APIKey struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	ProjectID  int64     `json:"project_id"`
	KeySHA256  string    `json:"key_sha256"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// APIKeyTouch records that an API key authenticated a request at At. Readers
// batch these and send them to the writer, the only process that can write.
type APIKeyTouch struct {
	KeySHA256 string    `json:"sha256"`
	At        time.Time `json:"at"`
}

// Key request statuses. "expired" is derived from ExpiresAt, never stored.
const (
	KeyRequestPending  = "pending"
	KeyRequestApproved = "approved"
	KeyRequestDenied   = "denied"
	KeyRequestClaimed  = "claimed"
	KeyRequestExpired  = "expired"
)

// KeyRequest is an agent's request for an ingest key for an active project. It
// carries no key material: the key is minted when the approved request is claimed.
type KeyRequest struct {
	ID          string    `json:"id"`
	ProjectID   int64     `json:"projectId"`
	ProjectSlug string    `json:"project"`
	Label       string    `json:"label"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// Release represents a software release row.
type Release struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
	ObservedAt  time.Time `json:"observed_at"`
	Version     string    `json:"version"`
	CommitSHA   string    `json:"commit_sha"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// Alert represents a project alert row.
type Alert struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	Severity        string         `json:"severity,omitempty"`
	Rule            map[string]any `json:"rule,omitempty"`
	WebhookURL      string         `json:"webhook_url,omitempty"`
	EmailTo         string         `json:"email_to,omitempty"`
	Condition       string         `json:"condition,omitempty"`
	Param           string         `json:"param,omitempty"`
	Threshold       int            `json:"threshold,omitempty"`
	CooldownMinutes int            `json:"cooldown_minutes,omitempty"`
	LastFiredAt     time.Time      `json:"last_fired_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
	ProjectSlug     string         `json:"project_slug,omitempty"`
}

// LogEntry represents a single structured log line stored per project.
type LogEntry struct {
	ID          int64          `json:"id"`
	ProjectID   int64          `json:"project_id,omitempty"`
	ProjectSlug string         `json:"project_slug,omitempty"`
	ReceivedAt  time.Time      `json:"received_at"`
	LevelNum    int            `json:"level_num"`
	Level       string         `json:"level"`
	Message     string         `json:"message"`
	Data        map[string]any `json:"data,omitempty"`
}

// Setting represents a project setting row.
type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IssueRelease is one release an issue was seen in, counted over a sample of
// the issue's latest events.
type IssueRelease struct {
	Release   string    `json:"release"`
	Events    int       `json:"events"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// SurfaceActivity counts the issues that occur on one sign-up surface (the
// value of the `surface` attribute) and were seen recently. Facets are an
// issue-level set, so the numbers are issues, not events.
type SurfaceActivity struct {
	Surface  string `json:"surface"`
	Issues1d int    `json:"issues_24h"`
	Issues7d int    `json:"issues_7d"`
	Open     int    `json:"open"`
}

// User represents an admin user stored in the database.
type User struct {
	ID             int64     `json:"id"`
	Username       string    `json:"username"`
	PasswordBcrypt string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SourceMap represents a stored source map file.
type SourceMap struct {
	ID          string    `json:"id"`
	Release     string    `json:"release"`
	Dist        string    `json:"dist"`
	BundleURL   string    `json:"bundle_url"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedAt  time.Time `json:"uploaded_at"`
}

// SourceMapMeta holds the metadata columns for a source map row (no blob).
type SourceMapMeta struct {
	ID        string    `json:"id"`
	Release   string    `json:"release"`
	Dist      string    `json:"dist"`
	BundleURL string    `json:"bundleUrl"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

// SourceMapUpload represents a source map upload request.
type SourceMapUpload struct {
	Release     string `json:"release"`
	Dist        string `json:"dist"`
	BundleURL   string `json:"bundle_url"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Blob        []byte `json:"-"`
}

// DigestIssue is a summary of a single issue for the weekly digest.
type DigestIssue struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	EventCount int    `json:"event_count"`
	Status     string `json:"status"`
}

// DigestData holds aggregate stats for the weekly digest.
type DigestData struct {
	TotalEvents    int           `json:"total_events"`
	NewIssues      int           `json:"new_issues"`
	ResolvedIssues int           `json:"resolved_issues"`
	Regressions    int           `json:"regressions"`
	TopIssues      []DigestIssue `json:"top_issues"`
}

// Monitor states and check-in statuses. A monitor is "ok" until a check-in
// reports an error or the miss detector finds it overdue.
const (
	MonitorStateOK     = "ok"
	MonitorStateMissed = "missed"
	MonitorStateError  = "error"

	CheckInOK    = "ok"
	CheckInError = "error"
)

// Monitor is a scheduled job or worker that reports in with check-ins. Only the
// latest signal is kept, not a history.
type Monitor struct {
	ID          int64
	ProjectID   int64
	ProjectSlug string // set by ListEnabledMonitors
	Slug        string
	Interval    time.Duration
	Grace       time.Duration
	// Schedule is a 5-field cron expression evaluated in Timezone. When set it
	// replaces Interval: the job is expected at each scheduled time.
	Schedule      string
	Timezone      string
	Enabled       bool
	CreatedAt     time.Time
	LastCheckInAt time.Time
	LastStatus    string
	State         string
	StateSince    time.Time
	LastEmittedAt time.Time
}

// NextDue is the time after which the monitor counts as missed. For an
// interval monitor it is the last check-in (or its creation, before the first
// one) plus interval and grace. For a cron monitor it is the first scheduled
// run after that check-in, plus grace: a check-in that arrives late, after the
// next run was already due, still answers the run it belongs to.
func (m Monitor) NextDue() time.Time {
	base := m.LastCheckInAt
	if base.IsZero() {
		base = m.CreatedAt
	}
	if m.Schedule == "" {
		return base.Add(m.Interval + m.Grace)
	}
	sched, err := cronexpr.Parse(m.Schedule)
	if err != nil {
		return base.Add(m.Interval + m.Grace)
	}
	next := sched.Next(base, m.Location())
	if next.IsZero() {
		return base.Add(m.Interval + m.Grace)
	}
	return next.Add(m.Grace)
}

// Location is the time zone of the schedule, UTC when none is set or the name
// is unknown to this build.
func (m Monitor) Location() *time.Location {
	if m.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(m.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// MonitorReportRepeat is how long an unresolved incident waits before it is
// reported again.
const MonitorReportRepeat = 24 * time.Hour

// NeedsReport reports whether the incident the monitor is in has not been
// reported yet, or was last reported MonitorReportRepeat ago. A monitor in
// state ok has no incident.
func (m Monitor) NeedsReport(now time.Time) bool {
	if m.State == MonitorStateOK {
		return false
	}
	return m.LastEmittedAt.Before(m.StateSince) || now.Sub(m.LastEmittedAt) >= MonitorReportRepeat
}
