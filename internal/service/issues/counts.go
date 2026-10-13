package issues

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// countsTTL is how long a per-scope status count is reused. The issues page
// shows these as totals, and a minute of lag is fine; resolving, muting or
// reopening through this process drops the cache so the acting user sees the
// change at once.
const countsTTL = time.Minute

// countsRefreshTimeout bounds one refresh. The refresh runs detached from the
// request, so a client that hangs up mid-fetch cannot leave the cache empty.
const countsRefreshTimeout = 10 * time.Second

// countsMaxScopes caps the cache. Scopes are projects and project groups, so
// the cap is far above real use; it only stops unbounded growth.
const countsMaxScopes = 256

type countsEntry struct {
	counts  domain.IssueCounts
	fetched time.Time
}

type countsCache struct {
	mu      sync.Mutex
	entries map[string]countsEntry
}

// scopeKey names the project or group scope carried by ctx.
func scopeKey(ctx context.Context) string {
	if ids, ok := domain.ProjectIDsFromContext(ctx); ok {
		sorted := slices.Clone(ids)
		slices.Sort(sorted)
		parts := make([]string, len(sorted))
		for i, id := range sorted {
			parts[i] = strconv.FormatInt(id, 10)
		}
		return "group:" + strings.Join(parts, ",")
	}
	if id, ok := domain.ProjectIDFromContext(ctx); ok {
		return fmt.Sprintf("project:%d", id)
	}
	return "default"
}

// Counts returns the per-status issue counts for the scope in ctx, from a cache
// that is at most countsTTL old. When a refresh fails and an older value exists,
// the older value is served: a stale total beats a failed page.
func (s *Service) Counts(ctx context.Context) (domain.IssueCounts, error) {
	key := scopeKey(ctx)
	c := &s.counts
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok && time.Since(e.fetched) < countsTTL {
		return e.counts, nil
	}

	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), countsRefreshTimeout)
	defer cancel()
	counts, err := s.repo.IssueStatusCounts(refreshCtx)
	if err != nil {
		if e, ok := c.entries[key]; ok {
			s.logger.WarnContext(ctx, "serving stale issue counts: refresh failed", "scope", key, "error", err)
			return e.counts, nil
		}
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "count issues by status", "scope", key, "error", err)
		}
		return domain.IssueCounts{}, err
	}
	if c.entries == nil || len(c.entries) >= countsMaxScopes {
		c.entries = make(map[string]countsEntry)
	}
	c.entries[key] = countsEntry{counts: counts, fetched: time.Now()}
	return counts, nil
}

// invalidateCounts drops every cached count after a status change.
func (s *Service) invalidateCounts() {
	s.counts.mu.Lock()
	s.counts.entries = nil
	s.counts.mu.Unlock()
}
