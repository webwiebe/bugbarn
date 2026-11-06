package telemetrydb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"modernc.org/sqlite"
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

// spinUntilExpired never finishes on its own; the context stops it. Its first
// row calls span_test_running, which tells the test the statement is running
// inside the driver.
const spinUntilExpired = `WITH RECURSIVE c(x) AS (SELECT span_test_running(?) UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c`

// running holds, per statement name, the channel span_test_running signals.
var running sync.Map

func init() {
	sqlite.MustRegisterScalarFunction("span_test_running", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		name, _ := args[0].(string)
		if ch, ok := running.Load(name); ok {
			select {
			case ch.(chan struct{}) <- struct{}{}:
			default:
			}
		}
		return int64(1), nil
	})
}

// expiringContext is a context whose deadline passes when expire is called. A
// timer deadline can pass before the statement reaches the driver, while
// database/sql still waits for the handle's one connection, and then no span
// is made at all. This one passes only once the statement is running.
type expiringContext struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newExpiringContext() *expiringContext {
	return &expiringContext{Context: context.Background(), done: make(chan struct{})}
}

func (c *expiringContext) Done() <-chan struct{} { return c.done }

func (c *expiringContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *expiringContext) expire() { c.once.Do(func() { close(c.done) }) }

type statementRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func queryUntilExpired(ctx context.Context, db statementRunner, q, name string) error {
	var n int
	return db.QueryRowContext(ctx, q, name).Scan(&n)
}

func execUntilExpired(ctx context.Context, db statementRunner, q, name string) error {
	_, err := db.ExecContext(ctx, q, name)
	return err
}

// runUntilExpired runs q on its own connection and lets the context's deadline
// pass once span_test_running reports the statement inside the driver.
func runUntilExpired(t *testing.T, db *sql.DB, name, q string, run func(context.Context, statementRunner, string, string) error) error {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	started := make(chan struct{}, 1)
	running.Store(name, started)
	defer running.Delete(name)

	ctx := newExpiringContext()
	defer ctx.expire()
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, conn, q, name) }()
	select {
	case <-started:
	case err := <-errc:
		t.Fatalf("the statement ended before it ran: %v", err)
	}
	ctx.expire()
	return <-errc
}

func TestDeadlineMarksSpanOnlyOutsideTheWriteHandle(t *testing.T) {
	exporter := sharedSpanExporter()
	d := openTest(t, Metrics, 0)

	cases := []struct {
		name     string
		db       *sql.DB
		run      func(context.Context, statementRunner, string, string) error
		wantCode codes.Code
	}{
		{"write handle query", d.Write(), queryUntilExpired, codes.Unset},
		{"write handle exec", d.Write(), execUntilExpired, codes.Unset},
		{"read handle query", d.Read(), queryUntilExpired, codes.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exporter.Reset()
			q := spinUntilExpired + " -- " + tc.name
			if err := runUntilExpired(t, tc.db, tc.name, q, tc.run); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want context deadline exceeded", err)
			}
			// The statement reached the driver, so exactly one span must carry
			// it. A dropped span fails here instead of passing unseen.
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
	var found []tracetest.SpanStub
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			if strings.HasSuffix(string(attr.Key), "statement") && attr.Value.AsString() == q {
				found = append(found, span)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d spans with statement %q, want 1", len(found), q)
	}
	return found[0]
}
