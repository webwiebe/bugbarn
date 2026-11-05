package domain

import "context"

type ctxRequestInfoKey struct{}

// RequestInfo names the HTTP request a service call runs for, so a service log
// line can say which caller met a failure.
type RequestInfo struct {
	// Route is the method and path, for example "POST /api/v1/monitors/x/check-in".
	Route     string
	UserAgent string
}

// WithRequestInfo returns a context carrying info.
func WithRequestInfo(ctx context.Context, info RequestInfo) context.Context {
	return context.WithValue(ctx, ctxRequestInfoKey{}, info)
}

// RequestInfoFromContext returns the RequestInfo set by WithRequestInfo, or the
// zero value when the call does not run for an HTTP request.
func RequestInfoFromContext(ctx context.Context) RequestInfo {
	info, _ := ctx.Value(ctxRequestInfoKey{}).(RequestInfo)
	return info
}
