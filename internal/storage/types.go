package storage

import (
	"database/sql"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

// Store is the primary database access object.
type Store struct {
	db               *sql.DB
	defaultProjectID int64
}

// Issue represents a grouped error occurrence.
type Issue struct {
	ID                     string
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

// IssueHourlyCounts holds per-issue 24-hour event frequency data.
type IssueHourlyCounts struct {
	IssueID string
	Counts  [24]int // index 0 = 23h ago, index 23 = current partial hour
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

// IssueFilter holds optional filters and sort order for ListIssuesFiltered.
type IssueFilter struct {
	// Sort is one of "last_seen", "first_seen", "event_count". Default: "last_seen".
	Sort string
	// Status filters by issue status:
	//   "open"     → status IN ('unresolved', 'regressed')  (default, excludes muted)
	//   "muted"    → status = 'muted'
	//   "resolved" → status = 'resolved'
	//   "all" or "" → no status filter
	Status string
	// Query is a case-insensitive substring matched against title and normalized_title.
	Query string
	// Facets is an optional map of facet key→value pairs to filter by.
	// Issues must match ALL provided facet filters (AND semantics).
	Facets map[string]string
	// Limit caps the number of returned issues. 0 means no limit.
	Limit int
	// Offset skips the first N results (for pagination).
	Offset int
}

// User represents an admin user stored in the database.
type User struct {
	ID             int64
	Username       string
	PasswordBcrypt string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Project represents a project row.
type Project struct {
	ID        int64
	Name      string
	Slug      string
	Status    string
	CreatedAt time.Time
}

// Scope constants for API keys.
const (
	APIKeyScopeFull   = "full"   // full access to all endpoints
	APIKeyScopeIngest = "ingest" // write-only: POST /api/v1/events only
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

// Setting represents a project setting row.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// facetRow is an internal struct used when inserting raw facets.
type facetRow struct {
	section string
	key     string
	value   string
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
