package telemetrydb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var (
	spanExporterOnce sync.Once
	spanExporter     *tracetest.InMemoryExporter
)

// sharedSpanExporter installs one global provider per test binary: the traced
// drivers register once and hold the global tracer, which delegates only to the
// first provider set.
func sharedSpanExporter() *tracetest.InMemoryExporter {
	spanExporterOnce.Do(func() {
		spanExporter = tracetest.NewInMemoryExporter()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter)))
	})
	return spanExporter
}

// spinUntilDeadline never finishes on its own; the context deadline stops it.
const spinUntilDeadline = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c`

func queryUntilDeadline(ctx context.Context, db *sql.DB, q string) error {
	var n int
	return db.QueryRowContext(ctx, q).Scan(&n)
}

func execUntilDeadline(ctx context.Context, db *sql.DB, q string) error {
	_, err := db.ExecContext(ctx, q)
	return err
}

func TestDeadlineMarksSpanOnlyOutsideTheWriteHandle(t *testing.T) {
	exporter := sharedSpanExporter()
	d := openTest(t, Metrics, 0)

	cases := []struct {
		name     string
		db       *sql.DB
		run      func(context.Context, *sql.DB, string) error
		wantCode codes.Code
	}{
		{"write handle query", d.Write(), queryUntilDeadline, codes.Unset},
		{"write handle exec", d.Write(), execUntilDeadline, codes.Unset},
		{"read handle query", d.Read(), queryUntilDeadline, codes.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter.Reset()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			q := spinUntilDeadline + " -- " + tc.name
			if err := tc.run(ctx, tc.db, q); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want context deadline exceeded", err)
			}
			span := spanWithStatement(t, exporter, q)
			if span.Status.Code != tc.wantCode {
				t.Errorf("status = %v, want %v", span.Status.Code, tc.wantCode)
			}
			if tc.wantCode == codes.Unset && len(span.Events) != 0 {
				t.Errorf("events = %v, want none", span.Events)
			}
		})
	}
}

func TestWriteHandleStillMarksOtherErrors(t *testing.T) {
	exporter := sharedSpanExporter()
	d := openTest(t, Metrics, 0)
	exporter.Reset()

	const q = `DELETE FROM no_such_table`
	if _, err := d.Write().ExecContext(context.Background(), q); err == nil {
		t.Fatal("exec on a missing table returned no error")
	}
	if got := spanWithStatement(t, exporter, q).Status.Code; got != codes.Error {
		t.Errorf("status = %v, want %v", got, codes.Error)
	}
}

func spanWithStatement(t *testing.T, exporter *tracetest.InMemoryExporter, q string) tracetest.SpanStub {
	t.Helper()
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			if strings.HasSuffix(string(attr.Key), "statement") && attr.Value.AsString() == q {
				return span
			}
		}
	}
	t.Fatalf("no span with statement %q", q)
	return tracetest.SpanStub{}
}
