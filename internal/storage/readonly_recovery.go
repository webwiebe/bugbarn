package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// readonlyRecoveryBackoffs are the waits before each retry of a read that failed
// with SQLITE_READONLY_RECOVERY (#242). Three retries, 175 ms in all.
var readonlyRecoveryBackoffs = []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond}

var readonlyRecoveryCounter, _ = tracing.Meter().Int64Counter(
	"bugbarn.storage.readonly_recovery",
	metric.WithDescription("Reads that hit SQLITE_READONLY_RECOVERY on a reader, by outcome (recovered or failed)."),
	metric.WithUnit("{read}"),
)

// IsReadonlyRecovery reports whether err is SQLITE_READONLY_RECOVERY (264): a
// read-only connection saw a wal-index that needs recovery and cannot run it.
func IsReadonlyRecovery(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqlite3.SQLITE_READONLY_RECOVERY
	}
	return err != nil && strings.Contains(err.Error(), "(264)")
}

// retryReadonlyRecovery runs fn and, while it fails with SQLITE_READONLY_RECOVERY,
// runs it again after a short wait. The first failure is recorded on the current
// span with the WAL file's size and age, so a failure can be placed against the
// writer's checkpoint ticks. The final error names the retry count.
func retryReadonlyRecovery(ctx context.Context, walPath string, fn func() error) error {
	err := fn()
	if !IsReadonlyRecovery(err) {
		return err
	}
	attrs := []attribute.KeyValue{attribute.String("error", err.Error())}
	if fi, statErr := os.Stat(walPath); statErr == nil {
		attrs = append(attrs,
			attribute.Int64("wal.bytes", fi.Size()),
			attribute.Float64("wal.age_seconds", time.Since(fi.ModTime()).Seconds()))
	}
	trace.SpanFromContext(ctx).AddEvent("sqlite.readonly_recovery", trace.WithAttributes(attrs...))

	for i, wait := range readonlyRecoveryBackoffs {
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		if err = fn(); !IsReadonlyRecovery(err) {
			readonlyRecoveryCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "recovered"), attribute.Int("retries", i+1)))
			return err
		}
	}
	readonlyRecoveryCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "failed")))
	return fmt.Errorf("after %d retries: %w", len(readonlyRecoveryBackoffs), err)
}
