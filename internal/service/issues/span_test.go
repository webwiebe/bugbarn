package issues

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// A lookup of a missing issue is a 404 for the caller, so it stays out of the
// error traces. A storage failure on the same call still marks the span.
func TestGetSpanStatus(t *testing.T) {
	cases := []struct {
		name string
		repo *fakeRepo
		want codes.Code
	}{
		{"not found", &fakeRepo{issues: map[string]domain.Issue{}}, codes.Unset},
		{"storage failure", &fakeRepo{err: errors.New("disk I/O error")}, codes.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			prev := otel.GetTracerProvider()
			otel.SetTracerProvider(tp)
			t.Cleanup(func() { otel.SetTracerProvider(prev) })

			if _, err := New(tc.repo, nil).Get(context.Background(), "BRA-77"); err == nil {
				t.Fatal("Get returned no error")
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("exported %d spans; want 1", len(spans))
			}
			if got := spans[0].Status.Code; got != tc.want {
				t.Fatalf("status = %v; want %v", got, tc.want)
			}
		})
	}
}
