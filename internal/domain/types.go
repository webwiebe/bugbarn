package domain

import (
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// Issue represents a grouped error occurrence.
type Issue struct {
	ID                     string        `json:"id"`
	IssueNumber            int           `json:"issue_number"`
	Fingerprint            string        `json:"fingerprint"`
	FingerprintMaterial    string        `json:"fingerprint_material"`
	FingerprintExplanation []string      `json:"fingerprint_explanation"`
	Title                  string        `json:"title"`
	NormalizedTitle        string        `json:"normalized_title"`
	ExceptionType          string        `json:"exception_type,omitempty"`
	Status                 string        `json:"status"`
	MuteMode               string        `json:"mute_mode,omitempty"`
	ResolvedAt             time.Time     `json:"resolved_at"`
	ReopenedAt             time.Time     `json:"reopened_at"`
	LastRegressedAt        time.Time     `json:"last_regressed_at"`
	RegressionCount        int           `json:"regression_count"`
	FirstSeen              time.Time     `json:"first_seen"`
	LastSeen               time.Time     `json:"last_seen"`
	EventCount             int           `json:"event_count"`
	RepresentativeEvent    event.Event   `json:"representative_event"`
	ProjectSlug            string        `json:"project_slug,omitempty"`
}

// IssueFilter holds optional filters and sort order for listing issues.
type IssueFilter struct {
	Sort   string
	Status string
	Query  string
	Facets map[string]string
	Limit  int
	Offset int
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
}

// ProjectGroup represents a named collection of related projects.
type ProjectGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
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
	Condition       string         `json:"condition,omitempty"`
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
