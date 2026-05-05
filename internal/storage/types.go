package storage

import (
	"database/sql"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// Store is the primary database access object. It holds a single-connection
// read-write handle (db) for mutations and a multi-connection read-only handle
// (roDB) for queries. Both point at the same WAL-mode SQLite file.
type Store struct {
	db               *sql.DB
	roDB             *sql.DB
	defaultProjectID int64
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

// Scope constants for API keys.
const (
	APIKeyScopeFull   = domain.APIKeyScopeFull
	APIKeyScopeIngest = domain.APIKeyScopeIngest
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
