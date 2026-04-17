package storage

import "context"

// WithProjectID returns a context carrying the given project ID for use by Store methods.
func WithProjectID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxProjectKey{}, id)
}

// ProjectIDFromContext extracts the project ID stored by WithProjectID.
// Returns (id, true) when a positive project ID is present, (0, false) otherwise.
func ProjectIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxProjectKey{}).(int64)
	return id, ok && id > 0
}
