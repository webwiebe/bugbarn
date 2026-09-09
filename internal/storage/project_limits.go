package storage

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// projectLimits is a project's volume policy: how long its events are kept and
// whether its issues are sampled. Both are overrides — a nil RetentionDays and
// an empty Sampling both mean "inherit whatever the deployment is configured
// with".
type projectLimits struct {
	RetentionDays *int
	Sampling      string // "" inherit | "on" | "off"
}

// Sampling modes. Stored as text so the column reads as itself in a shell.
const (
	SamplingInherit = ""
	SamplingOn      = "on"
	SamplingOff     = "off"
)

// limitsCacheTTL bounds how stale the write path's view of a project's policy
// can be. The ingest hot path consults this for every event, and the lesson from
// the facet key-count stall (46s per event holding the single write connection)
// is that per-event queries are how ingest dies. A project's policy changes
// approximately never, so half a minute of staleness after a settings change is
// a fair trade for touching the database once per project per 30s. A write
// through UpdateProjectLimits invalidates the entry immediately, so the delay is
// only ever visible to a *different* process than the one that made the change.
const limitsCacheTTL = 30 * time.Second

type limitsCacheEntry struct {
	limits    projectLimits
	fetchedAt time.Time
}

// limitsCache is a per-process cache of project volume policy.
type limitsCache struct {
	mu      sync.RWMutex
	entries map[int64]limitsCacheEntry
	now     func() time.Time // overridable in tests
}

func newLimitsCache() *limitsCache {
	return &limitsCache{entries: make(map[int64]limitsCacheEntry), now: time.Now}
}

func (c *limitsCache) get(projectID int64) (projectLimits, bool) {
	if c == nil {
		return projectLimits{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[projectID]
	if !ok || c.now().Sub(entry.fetchedAt) > limitsCacheTTL {
		return projectLimits{}, false
	}
	return entry.limits, true
}

func (c *limitsCache) put(projectID int64, limits projectLimits) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[projectID] = limitsCacheEntry{limits: limits, fetchedAt: c.now()}
}

func (c *limitsCache) invalidate(projectID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, projectID)
}

// SetSampleAfter sets the deployment-wide sampling threshold. 0 disables event
// sampling entirely; projects that have explicitly opted in are still sampled,
// at the default threshold. Call it once at startup, before serving.
func (s *core) SetSampleAfter(n int64) {
	if n < 0 {
		n = 0
	}
	s.sampleAfter = n
}

// SampleAfter reports the deployment-wide sampling threshold, for the API to
// show alongside each project's override.
func (s *core) SampleAfter() int64 { return s.sampleAfter }

// limitsFor returns a project's volume policy, reading through the cache.
//
// A lookup failure yields the zero policy — inherit everything — rather than an
// error. This runs on the ingest path, and the correct response to "I could not
// read this project's sampling preference" is to store the event, not to drop it
// or to fail the write.
func (s *core) limitsFor(ctx context.Context, projectID int64) projectLimits {
	if projectID <= 0 {
		return projectLimits{}
	}
	if limits, ok := s.limits.get(projectID); ok {
		return limits
	}

	var (
		retention sql.NullInt64
		sampling  sql.NullString
	)
	if err := s.readDB().QueryRowContext(ctx,
		`SELECT retention_days, sampling_mode FROM projects WHERE id = ?`, projectID,
	).Scan(&retention, &sampling); err != nil {
		return projectLimits{}
	}

	limits := projectLimits{Sampling: sampling.String}
	if retention.Valid {
		days := int(retention.Int64)
		limits.RetentionDays = &days
	}
	s.limits.put(projectID, limits)
	return limits
}

// sampleAfterFor resolves the sampling threshold to apply to a project: the
// configured global threshold, or 0 (meaning "store everything") when the
// project has opted out.
func (s *core) sampleAfterFor(ctx context.Context, projectID int64) int64 {
	switch s.limitsFor(ctx, projectID).Sampling {
	case SamplingOff:
		return 0
	case SamplingOn:
		// An explicit opt-in still uses the deployment's threshold; the per-project
		// control is a switch, not a dial.
		if s.sampleAfter > 0 {
			return s.sampleAfter
		}
		return defaultSampleAfter
	default:
		return s.sampleAfter
	}
}

// UpdateProjectLimits sets a project's volume policy.
//
// retentionDays is nil to inherit the deployment window; the caller is expected
// to have clamped it, since a value longer than the global window is a promise
// the global sweep would break. sampling must be one of the Sampling* constants.
func (s *ProjectStore) UpdateProjectLimits(ctx context.Context, slug string, retentionDays *int, sampling string) error {
	switch sampling {
	case SamplingInherit, SamplingOn, SamplingOff:
	default:
		return apperr.InvalidInput("sampling_mode must be empty, \"on\" or \"off\"", nil)
	}
	if retentionDays != nil && *retentionDays < 1 {
		return apperr.InvalidInput("retention_days must be at least 1 day", nil)
	}

	var days sql.NullInt64
	if retentionDays != nil {
		days = sql.NullInt64{Int64: int64(*retentionDays), Valid: true}
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET retention_days = ?, sampling_mode = ? WHERE slug = ?`,
		days, sampling, slug)
	if err != nil {
		return wrapErr(err, "update project limits")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("project not found", nil)
	}

	// Drop the cached policy rather than update it: the next ingest re-reads the
	// row and cannot disagree with what was just written.
	var projectID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE slug = ?`, slug).Scan(&projectID); err == nil {
		s.limits.invalidate(projectID)
	}
	return nil
}

// ProjectsWithRetentionOverride returns the id and window of every project that
// has its own retention window, for the retention sweep's per-project pass.
func (s *ProjectStore) ProjectsWithRetentionOverride(ctx context.Context) (map[int64]int, error) {
	rows, err := s.readDB().QueryContext(ctx,
		`SELECT id, retention_days FROM projects WHERE retention_days IS NOT NULL`)
	if err != nil {
		return nil, wrapErr(err, "list project retention overrides")
	}
	defer rows.Close()

	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var days int
		if err := rows.Scan(&id, &days); err != nil {
			return nil, wrapErr(err, "list project retention overrides")
		}
		out[id] = days
	}
	return out, rows.Err()
}
