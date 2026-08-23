package projects

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// usageTTL is how long a computed usage snapshot is reused.
//
// The underlying query aggregates three of the largest tables in the database
// with no WHERE clause, so it costs on the order of a second and gets more
// expensive as the data grows. The numbers it produces are decorative — they
// are rendered as "how much is in here" on the settings screen and nothing
// branches on them — so serving one that is up to a minute old is free, while
// recomputing it per request was the single slowest thing the service did.
const usageTTL = time.Minute

// usageCache memoizes the per-project usage aggregate.
//
// The mutex is held across the refresh on purpose: it collapses a burst of
// concurrent callers into one query instead of letting each start its own
// full-table scan. That coalescing matters more than the lock wait — the
// endpoint is polled continuously, and before this the overlapping scans
// competed for the read pool and timed each other out.
type usageCache struct {
	mu      sync.Mutex
	value   map[int64]storage.ProjectUsage
	fetched time.Time
}

type usageFetch func(context.Context) (map[int64]storage.ProjectUsage, error)

// usageRefreshTimeout bounds one refresh. Generous relative to the query (which
// costs on the order of seconds) but finite, so a wedged read can never pin the
// cache mutex indefinitely.
const usageRefreshTimeout = 30 * time.Second

// get returns a usage snapshot, recomputing it only when the cached one has
// aged out. The second return value reports whether the snapshot is stale — a
// refresh failed and a previously cached value is being served instead.
//
// When a refresh fails and nothing is cached, the error is returned rather than
// an empty map. An empty map is indistinguishable from "every project has zero
// events", which is exactly the silent-wrong-data failure this replaces: the
// handler used to discard this error and render zeros.
func (c *usageCache) get(ctx context.Context, fetch usageFetch) (map[int64]storage.ProjectUsage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Keyed on fetched, not on value: a legitimately empty result (no projects
	// yet) is still a valid snapshot and must not force a re-query every call.
	if !c.fetched.IsZero() && time.Since(c.fetched) < usageTTL {
		return c.value, false, nil
	}

	// The refresh deliberately does not inherit the caller's cancellation.
	// This refill is shared by everyone, and in production the callers that
	// trigger it give up in well under a second while the query takes one to
	// four — so binding it to the first caller's context would cancel every
	// refresh and the cache would never fill, which is precisely the situation
	// it exists to fix. WithoutCancel keeps the trace context and request
	// values; the timeout keeps it bounded.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageRefreshTimeout)
	defer cancel()

	fresh, err := fetch(refreshCtx)
	if err != nil {
		if !c.fetched.IsZero() {
			// A stale-but-real snapshot beats failing the request. The caller
			// is told it is stale so it can say so rather than present it as
			// current.
			return c.value, true, nil
		}
		return nil, false, err
	}

	c.value = fresh
	c.fetched = time.Now()
	return fresh, false, nil
}

// invalidate drops the cached snapshot so the next read recomputes it. Called
// after mutations that visibly change the counts, so the settings screen does
// not show a deleted project's rows for up to a minute after the fact.
func (c *usageCache) invalidate() {
	c.mu.Lock()
	c.value = nil
	c.fetched = time.Time{}
	c.mu.Unlock()
}

// UsageAll returns per-project issue, event, and log counts, served from a
// short-lived cache. The bool reports whether the snapshot is stale (a refresh
// failed and an older one is being served).
func (s *Service) UsageAll(ctx context.Context) (map[int64]storage.ProjectUsage, bool, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.projects.UsageAll")
	defer span.End()

	usage, stale, err := s.usage.get(ctx, s.repo.ProjectUsageAll)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		// Context errors mean the caller went away mid-query, which is not a
		// fault of ours; logging them at error level self-reports a bug on
		// every client disconnect.
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "usage all", "error", err)
		}
		return nil, false, err
	}
	span.SetAttributes(attribute.Bool("usage.stale", stale))
	if stale {
		s.logger.WarnContext(ctx, "serving stale project usage: refresh failed")
	}
	return usage, stale, nil
}
