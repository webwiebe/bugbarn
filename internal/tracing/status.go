package tracing

import (
	"errors"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// SetError marks a span as failed. A not-found result leaves the status unset:
// the API answers it with a 404, and an error status would put a caller's lookup
// of a missing record in the error traces and past the tail sampler.
func SetError(span trace.Span, err error) {
	if errors.Is(err, apperr.ErrNotFound) {
		return
	}
	span.SetStatus(codes.Error, err.Error())
}
