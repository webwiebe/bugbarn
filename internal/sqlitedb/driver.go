// Package sqlitedb is the SQLite plumbing shared by every database file the
// service opens: bugbarn.db (internal/storage) and the telemetry files
// security.db and metrics.db (internal/telemetrydb).
//
// It owns three things:
//
//   - the otelsql-wrapped driver names (DriverName, BudgetedDriverName) and the
//     read-only recovery driver (ReadOnlyDriverName), which retries a read that
//     fails with SQLITE_READONLY_RECOVERY, CANTINIT or PROTOCOL and counts the
//     outcome in bugbarn.storage.readonly_recovery;
//   - the WAL checkpoint mechanics that operate on a *sql.DB: CheckpointDB, the
//     periodic loop (RunCheckpointLoop) and its CheckpointPolicy;
//   - IsReadonlyRecovery, the classifier for the errors above.
//
// Nothing here knows the schema or the domain of any one file. Error mapping to
// apperr stays in the packages that own a file.
package sqlitedb

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	_ "modernc.org/sqlite"
)

// BaseDriverName is the name modernc.org/sqlite registers itself under.
const BaseDriverName = "sqlite"

var (
	registerDriverOnce sync.Once
	registeredDriver   = BaseDriverName

	registerBudgetedOnce sync.Once
	budgetedDriver       = BaseDriverName
)

// registerTraced registers an otelsql-wrapped SQLite driver and returns its
// name. Falls back to the plain driver if registration fails. recordError
// decides which errors mark a span as failed; nil records all of them.
func registerTraced(recordError func(error) bool) string {
	return registerTracedOn(BaseDriverName, recordError)
}

// registerTracedOn is registerTraced over the registered driver base.
func registerTracedOn(base string, recordError func(error) bool) string {
	name, err := otelsql.Register(base,
		otelsql.WithAttributes(semconv.DBSystemSqlite),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			OmitConnResetSession: true,
			OmitConnPrepare:      true,
			OmitRows:             true,
			RecordError:          recordError,
		}),
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	)
	if err != nil {
		slog.Warn("otelsql driver registration failed; falling back to plain driver", "err", err)
		return base
	}
	return name
}

// DriverName returns the otelsql-wrapped SQLite driver name, registering it on
// first use. The main database and the telemetry files share it, so they get
// the same tracing.
func DriverName() string {
	registerDriverOnce.Do(func() { registeredDriver = registerTraced(nil) })
	return registeredDriver
}

// BudgetedDriverName returns a driver with the same tracing as DriverName,
// except that an expired context deadline does not mark the span as failed.
// It is for handles whose work runs under a deadline that ends it on purpose,
// such as the telemetry maintenance pass, where every expiry would otherwise
// count as an error trace. The span keeps its statement and duration, and the
// pass still logs what it could not finish.
func BudgetedDriverName() string {
	registerBudgetedOnce.Do(func() {
		budgetedDriver = registerTraced(func(err error) bool {
			return !errors.Is(err, context.DeadlineExceeded)
		})
	})
	return budgetedDriver
}
