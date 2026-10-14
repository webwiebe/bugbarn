// Package timeline builds the stacked error-event chart of the Issues page:
// error events per project over a time range, zero-filled into fixed buckets.
package timeline

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// Repository is the storage surface the service needs.
type Repository interface {
	ErrorTimelineRows(ctx context.Context, since time.Time) ([]storage.TimelineRow, error)
}

// rangeSpec describes one selectable range: the bucket width and bucket count.
type rangeSpec struct {
	step  time.Duration
	count int
}

var ranges = map[string]rangeSpec{
	"24h": {step: time.Hour, count: 24},
	"7d":  {step: 6 * time.Hour, count: 28},
	"30d": {step: 24 * time.Hour, count: 30},
}

// DefaultRange is used when the caller names no range.
const DefaultRange = "24h"

// cacheTTL is how long a computed timeline is reused. The chart is a trend
// overview, so a half-minute old answer costs nothing and a page of readers
// polling it shares one aggregate query.
const cacheTTL = 30 * time.Second

// refreshTimeout bounds one shared refresh.
const refreshTimeout = 20 * time.Second

// maxCacheEntries bounds the cache across ranges and request scopes.
const maxCacheEntries = 64

// Bucket is one time slot. Counts holds the projects that had error events in
// the slot, keyed by slug; a slot without errors has an empty map.
type Bucket struct {
	Start  time.Time        `json:"start"`
	Counts map[string]int64 `json:"counts"`
}

// Project is a project that appears in the range, with its total.
type Project struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Total int64  `json:"total"`
}

// Timeline is the answer for one range.
type Timeline struct {
	Range         string    `json:"range"`
	BucketSeconds int64     `json:"bucket_seconds"`
	Buckets       []Bucket  `json:"buckets"`
	Projects      []Project `json:"projects"`
}

type entry struct {
	value   Timeline
	fetched time.Time
}

// Service computes and caches the timeline.
type Service struct {
	repo   Repository
	logger *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

// New returns a timeline service.
func New(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger.With("service", "timeline"), now: time.Now, cache: map[string]entry{}}
}

// ErrorTimeline returns the error events per project for the range ("24h",
// "7d" or "30d"; empty means 24h). The request's project scope applies.
func (s *Service) ErrorTimeline(ctx context.Context, rangeName string) (Timeline, error) {
	if rangeName == "" {
		rangeName = DefaultRange
	}
	spec, ok := ranges[rangeName]
	if !ok {
		return Timeline{}, apperr.InvalidInput("range must be 24h, 7d or 30d", nil)
	}
	ctx, span := tracing.Tracer().Start(ctx, "service.timeline.ErrorTimeline")
	defer span.End()

	key := cacheKey(ctx, rangeName)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.cache[key]; ok && s.now().Sub(e.fetched) < cacheTTL {
		return e.value, nil
	}

	// The refresh is shared by every caller, so it does not follow the first
	// caller's cancellation; the timeout keeps it bounded.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	now := s.now().UTC()
	first := alignedStart(now, spec).Add(-time.Duration(spec.count-1) * spec.step)
	rows, err := s.repo.ErrorTimelineRows(refreshCtx, first)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if !apperr.IsContextError(err) {
			s.logger.ErrorContext(ctx, "error timeline", "range", rangeName, "error", err)
		}
		if e, ok := s.cache[key]; ok {
			return e.value, nil
		}
		return Timeline{}, err
	}

	value := build(rangeName, spec, first, rows)
	if len(s.cache) >= maxCacheEntries {
		s.cache = map[string]entry{}
	}
	s.cache[key] = entry{value: value, fetched: s.now()}
	return value, nil
}

func cacheKey(ctx context.Context, rangeName string) string {
	id, _ := domain.ProjectIDFromContext(ctx)
	ids, _ := domain.ProjectIDsFromContext(ctx)
	return fmt.Sprintf("%s|%d|%v", rangeName, id, ids)
}

// alignedStart returns the start of the bucket that contains t, with buckets
// aligned to UTC midnight so that 6h buckets begin at 00, 06, 12 and 18.
func alignedStart(t time.Time, spec rangeSpec) time.Time {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	since := t.Sub(midnight)
	return midnight.Add(since - since%spec.step)
}

// build folds hourly rows into zero-filled buckets starting at first.
func build(rangeName string, spec rangeSpec, first time.Time, rows []storage.TimelineRow) Timeline {
	out := Timeline{
		Range:         rangeName,
		BucketSeconds: int64(spec.step / time.Second),
		Buckets:       make([]Bucket, spec.count),
		Projects:      []Project{},
	}
	for i := range out.Buckets {
		out.Buckets[i] = Bucket{Start: first.Add(time.Duration(i) * spec.step), Counts: map[string]int64{}}
	}
	totals := map[string]*Project{}
	for _, r := range rows {
		idx := int(r.Hour.Sub(first) / spec.step)
		if r.Hour.Before(first) || idx >= spec.count {
			continue
		}
		out.Buckets[idx].Counts[r.ProjectSlug] += r.Count
		p := totals[r.ProjectSlug]
		if p == nil {
			p = &Project{Slug: r.ProjectSlug, Name: r.ProjectName}
			totals[r.ProjectSlug] = p
		}
		p.Total += r.Count
	}
	for _, p := range totals {
		out.Projects = append(out.Projects, *p)
	}
	sort.Slice(out.Projects, func(i, j int) bool {
		if out.Projects[i].Total != out.Projects[j].Total {
			return out.Projects[i].Total > out.Projects[j].Total
		}
		return out.Projects[i].Slug < out.Projects[j].Slug
	})
	return out
}
