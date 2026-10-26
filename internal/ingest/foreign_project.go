package ingest

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// Measurement for #235: before ingest binds a project-bound key to its project,
// count the requests where such a key names another project in
// X-BugBarn-Project. The counter reads 0 for a few days before the binding goes in.

var foreignProjectHeaderCounter, _ = tracing.Meter().Int64Counter(
	"bugbarn.ingest.foreign_project_header",
	metric.WithDescription("Requests where a project-bound, non-full-scope API key named another project in X-BugBarn-Project."),
	metric.WithUnit("{request}"),
)

// maxForeignPairs bounds the set of logged (key project, header project) pairs,
// because the header value is client-controlled.
const maxForeignPairs = 1000

// foreignProjectObserver resolves a project ID to its slug, cached per ID.
type foreignProjectObserver struct {
	slugOf func(ctx context.Context, projectID int64) (string, error)
	slugs  sync.Map // int64 -> string

	pairsMu sync.Mutex
	pairs   map[string]struct{}
}

// SetProjectSlugResolver enables the foreign-project-header measurement. fn maps
// a project ID to its slug; without it the handler does not measure.
func (h *Handler) SetProjectSlugResolver(fn func(ctx context.Context, projectID int64) (string, error)) {
	h.foreign = &foreignProjectObserver{slugOf: fn, pairs: make(map[string]struct{})}
}

// observeProjectHeader counts a request whose bound, non-full-scope key names a
// project other than its own. Lookup failures are ignored: this is a measurement.
func (h *Handler) observeProjectHeader(ctx context.Context, projectID int64, scope, headerSlug string) {
	o := h.foreign
	if o == nil || projectID <= 0 || headerSlug == "" || scope == domain.APIKeyScopeFull {
		return
	}
	own, ok := o.slugs.Load(projectID)
	if !ok {
		slug, err := o.slugOf(ctx, projectID)
		if err != nil || slug == "" {
			return
		}
		o.slugs.Store(projectID, slug)
		own = slug
	}
	ownSlug, _ := own.(string)
	if ownSlug == headerSlug {
		return
	}
	foreignProjectHeaderCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("key_project", ownSlug), attribute.String("scope", scope)))
	if o.firstOfPair(ownSlug + "->" + headerSlug) {
		h.logger.InfoContext(ctx, "bound api key named another project",
			"key_project", ownSlug, "header_project", headerSlug, "scope", scope)
	}
}

// firstOfPair reports whether pair is new, within the bound on tracked pairs.
func (o *foreignProjectObserver) firstOfPair(pair string) bool {
	o.pairsMu.Lock()
	defer o.pairsMu.Unlock()
	if _, seen := o.pairs[pair]; seen || len(o.pairs) >= maxForeignPairs {
		return false
	}
	o.pairs[pair] = struct{}{}
	return true
}
