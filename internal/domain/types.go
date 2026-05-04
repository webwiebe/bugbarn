package domain

import (
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// Issue represents a grouped error occurrence.
type Issue struct {
	ID                     string
	IssueNumber            int
	Fingerprint            string
	FingerprintMaterial    string
	FingerprintExplanation []string
	Title                  string
	NormalizedTitle        string
	ExceptionType          string
	Status                 string
	MuteMode               string // "until_regression" | "forever" | ""
	ResolvedAt             time.Time
	ReopenedAt             time.Time
	LastRegressedAt        time.Time
	RegressionCount        int
	FirstSeen              time.Time
	LastSeen               time.Time
	EventCount             int
	RepresentativeEvent    event.Event
	ProjectSlug            string `json:"project_slug,omitempty"`
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
	ID                     string
	IssueID                string
	Fingerprint            string
	FingerprintMaterial    string
	FingerprintExplanation []string
	ReceivedAt             time.Time
	ObservedAt             time.Time
	Severity               string
	Message                string
	Regressed              bool
	Payload                event.Event
}

// Project represents a project row.
type Project struct {
	ID           int64
	Name         string
	Slug         string
	Status       string
	IssuePrefix  string
	IssueCounter int
	CreatedAt    time.Time
}

// Scope constants for API keys.
const (
	APIKeyScopeFull   = "full"
	APIKeyScopeIngest = "ingest"
)

// APIKey represents an API key row (the plaintext key is never stored).
type APIKey struct {
	ID         int64
	Name       string
	ProjectID  int64
	KeySHA256  string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// Release represents a software release row.
type Release struct {
	ID          string
	Name        string
	Environment string
	ObservedAt  time.Time
	Version     string
	CommitSHA   string
	URL         string
	Notes       string
	CreatedBy   string
	CreatedAt   time.Time
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
	Key       string
	Value     string
	UpdatedAt time.Time
}

// User represents an admin user stored in the database.
type User struct {
	ID             int64
	Username       string
	PasswordBcrypt string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SourceMap represents a stored source map file.
type SourceMap struct {
	ID          string
	Release     string
	Dist        string
	BundleURL   string
	Name        string
	ContentType string
	SizeBytes   int64
	UploadedAt  time.Time
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
	Release     string
	Dist        string
	BundleURL   string
	Name        string
	ContentType string
	Blob        []byte
}

// DigestIssue is a summary of a single issue for the weekly digest.
type DigestIssue struct {
	ID         string
	Title      string
	EventCount int
	Status     string
}

// DigestData holds aggregate stats for the weekly digest.
type DigestData struct {
	TotalEvents    int
	NewIssues      int
	ResolvedIssues int
	Regressions    int
	TopIssues      []DigestIssue
}
