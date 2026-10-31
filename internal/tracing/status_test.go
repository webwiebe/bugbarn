package tracing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestSetError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", apperr.NotFound("issue not found", nil), codes.Unset},
		{"wrapped not found", fmt.Errorf("get issue: %w", apperr.NotFound("issue not found", nil)), codes.Unset},
		{"internal", apperr.Internal("query failed", errors.New("disk I/O error")), codes.Error},
		{"invalid input", apperr.InvalidInput("bad id", nil), codes.Error},
		{"plain error", errors.New("boom"), codes.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &captureProcessor{}
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(capture))
			_, span := tp.Tracer("test").Start(context.Background(), "op")
			SetError(span, tc.err)
			span.End()
			if capture.count() != 1 {
				t.Fatalf("captured %d spans; want 1", capture.count())
			}
			if got := capture.spans[0].Status().Code; got != tc.want {
				t.Fatalf("status = %v; want %v", got, tc.want)
			}
		})
	}
}
