package storage

import (
	"database/sql"
	"sync/atomic"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// core holds the shared database state and the cross-domain write kernel
// (connection handles, schema init/migration, and the ingest write path that
// spans issues + events + facets). Every domain store embeds *core so it can
// reach the connections and the shared kernel, while exposing only its own
// domain's public methods.
type core struct {
	db               *sql.DB
	roDB             *sql.DB
	defaultProjectID int64

	// logInsertCount counts log-entry insert batches so the retention trim can
	// be amortized (run roughly once per logTrimInterval batches) instead of on
	// every insert — the single writer is shared with event ingestion, so we
	// keep each log write as short as possible.
	logInsertCount atomic.Uint64
}

// Domain stores. Each is a narrow, focused view over the shared core exposing
// only one domain model's operations. Services depend on these (or interfaces
// satisfied by them), not on the whole storage surface.
type (
	IssueStore      struct{ *core }
	EventStore      struct{ *core }
	ProjectStore    struct{ *core }
	GroupStore      struct{ *core }
	AlertStore      struct{ *core }
	ReleaseStore    struct{ *core }
	AnalyticsStore  struct{ *core }
	LogStore        struct{ *core }
	SettingsStore   struct{ *core }
	SourceMapStore  struct{ *core }
	APIKeyStore     struct{ *core }
	FacetStore      struct{ *core }
	HeldEventStore  struct{ *core }
	UserStore       struct{ *core }
	DigestStore     struct{ *core }
	WebSessionStore struct{ *core }
)

// Store is a thin facade that composes every domain store over one core. Its
// embedded fields promote each domain's methods plus the core kernel, so
// aggregate consumers (the ingest/worker paths) keep a single handle, while
// callers that want a narrow dependency take a domain field (e.g. s.IssueStore)
// or the typed handles from Domains().
type Store struct {
	*core
	*IssueStore
	*EventStore
	*ProjectStore
	*GroupStore
	*AlertStore
	*ReleaseStore
	*AnalyticsStore
	*LogStore
	*SettingsStore
	*SourceMapStore
	*APIKeyStore
	*FacetStore
	*HeldEventStore
	*UserStore
	*DigestStore
	*WebSessionStore
}

// Stores exposes the individual domain stores by name for dependency wiring.
type Stores struct {
	Issues     *IssueStore
	Events     *EventStore
	Projects   *ProjectStore
	Groups     *GroupStore
	Alerts     *AlertStore
	Releases   *ReleaseStore
	Analytics  *AnalyticsStore
	Logs       *LogStore
	Settings   *SettingsStore
	SourceMaps *SourceMapStore
	APIKeys    *APIKeyStore
	Facets     *FacetStore
	HeldEvents *HeldEventStore
	Users      *UserStore
	Digests    *DigestStore
}

// newStore builds the facade and every domain store over a single shared core.
func newStore(c *core) *Store {
	return &Store{
		core:            c,
		IssueStore:      &IssueStore{c},
		EventStore:      &EventStore{c},
		ProjectStore:    &ProjectStore{c},
		GroupStore:      &GroupStore{c},
		AlertStore:      &AlertStore{c},
		ReleaseStore:    &ReleaseStore{c},
		AnalyticsStore:  &AnalyticsStore{c},
		LogStore:        &LogStore{c},
		SettingsStore:   &SettingsStore{c},
		SourceMapStore:  &SourceMapStore{c},
		APIKeyStore:     &APIKeyStore{c},
		FacetStore:      &FacetStore{c},
		HeldEventStore:  &HeldEventStore{c},
		UserStore:       &UserStore{c},
		DigestStore:     &DigestStore{c},
		WebSessionStore: &WebSessionStore{c},
	}
}

// Domains returns the individual domain stores for narrow dependency wiring.
func (s *Store) Domains() Stores {
	return Stores{
		Issues:     s.IssueStore,
		Events:     s.EventStore,
		Projects:   s.ProjectStore,
		Groups:     s.GroupStore,
		Alerts:     s.AlertStore,
		Releases:   s.ReleaseStore,
		Analytics:  s.AnalyticsStore,
		Logs:       s.LogStore,
		Settings:   s.SettingsStore,
		SourceMaps: s.SourceMapStore,
		APIKeys:    s.APIKeyStore,
		Facets:     s.FacetStore,
		HeldEvents: s.HeldEventStore,
		Users:      s.UserStore,
		Digests:    s.DigestStore,
	}
}

// Domain type aliases — storage consumers can keep using storage.Issue etc.
// while the canonical definitions live in the domain package.
type Issue = domain.Issue
type IssueFilter = domain.IssueFilter
type Event = domain.Event
type Project = domain.Project
type APIKey = domain.APIKey
type Release = domain.Release
type Alert = domain.Alert
type LogEntry = domain.LogEntry
type Setting = domain.Setting
type User = domain.User
type SourceMap = domain.SourceMap
type SourceMapMeta = domain.SourceMapMeta
type SourceMapUpload = domain.SourceMapUpload
type DigestIssue = domain.DigestIssue
type DigestData = domain.DigestData
type ProjectGroup = domain.ProjectGroup
type ProjectAlias = domain.ProjectAlias

// Scope constants for API keys.
const (
	APIKeyScopeFull   = domain.APIKeyScopeFull
	APIKeyScopeIngest = domain.APIKeyScopeIngest
	APIKeyScopeRead   = domain.APIKeyScopeRead
)

// IssueHourlyCounts holds per-issue 24-hour event frequency data.
type IssueHourlyCounts struct {
	IssueID string
	Counts  [24]int // index 0 = 23h ago, index 23 = current partial hour
}

// facetRow is an internal struct used when inserting raw facets.
type facetRow struct {
	section string
	key     string
	value   string
}
