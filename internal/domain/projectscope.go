package domain

import "context"

type ctxProjectKey struct{}
type ctxProjectIDsKey struct{}

// projectIDVal wraps an int64 so that WithProjectID(ctx, 0) (all-projects) is
// distinguishable from a context that carries no project at all.
type projectIDVal struct{ id int64 }

// WithProjectID returns a context carrying the given project ID for use by Store
// methods. Pass 0 to signal "all projects" (session-based reads only).
func WithProjectID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxProjectKey{}, projectIDVal{id: id})
}

// ProjectIDFromContext extracts the project ID set by WithProjectID.
// Returns (id, true) when a project was explicitly set (id may be 0 = all projects).
// Returns (0, false) when no project was set at all (callers should fall back to defaultProjectID).
func ProjectIDFromContext(ctx context.Context) (int64, bool) {
	val, ok := ctx.Value(ctxProjectKey{}).(projectIDVal)
	return val.id, ok
}

// WithProjectIDs puts a set of project IDs in the context for group-scoped queries.
func WithProjectIDs(ctx context.Context, ids []int64) context.Context {
	return context.WithValue(ctx, ctxProjectIDsKey{}, ids)
}

// ProjectIDsFromContext returns the group-scoped project IDs, if set.
func ProjectIDsFromContext(ctx context.Context) ([]int64, bool) {
	ids, ok := ctx.Value(ctxProjectIDsKey{}).([]int64)
	return ids, ok && len(ids) > 0
}

type ctxIssueProjectKey struct{}

// WithIssueProject confines the lookup of an issue by its display id to one
// project. Admin writes use it so that an id which names an issue in another
// project can never be the one changed.
func WithIssueProject(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxIssueProjectKey{}, id)
}

// IssueProjectFromContext returns the project set by WithIssueProject.
func IssueProjectFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxIssueProjectKey{}).(int64)
	return id, ok && id > 0
}
