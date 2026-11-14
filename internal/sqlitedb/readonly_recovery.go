package sqlitedb

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

// readonlyRecoveryHook, when set, runs once after a read first fails with 264
// and before its retries. It is nil in production; a test uses it to make the
// writer write while the reader waits.
var readonlyRecoveryHook func()

// SetReadonlyRecoveryHook installs the hook described on readonlyRecoveryHook.
// It exists for tests in other packages that drive a reader through the retry;
// production code never calls it. Pass nil to remove the hook.
func SetReadonlyRecoveryHook(fn func()) { readonlyRecoveryHook = fn }

var readonlyRecoveryCounter, _ = tracing.Meter().Int64Counter(
	"bugbarn.storage.readonly_recovery",
	metric.WithDescription("Reads that hit SQLITE_READONLY_RECOVERY on a reader, by outcome (recovered or failed)."),
	metric.WithUnit("{read}"),
)

// IsReadonlyRecovery reports whether err is SQLITE_READONLY_RECOVERY (264) or
// SQLITE_READONLY_CANTINIT (1288): a read-only connection saw a wal-index that
// needs recovery or initialization and cannot do either. Only the writer can,
// so both clear on the writer's next write (#242, #268). SQLITE_PROTOCOL (15,
// "locking protocol") is the same family: a reader failed to take a wal-index lock
// while the writer was rewriting the index, and a later attempt succeeds (#275).
func IsReadonlyRecovery(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		c := sqliteErr.Code()
		return c == sqlite3.SQLITE_READONLY_RECOVERY ||
			c == sqlite3.SQLITE_READONLY_CANTINIT ||
			c == sqlite3.SQLITE_PROTOCOL
	}
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "(264)") || strings.Contains(msg, "(1288)") ||
		strings.Contains(msg, "locking protocol (15)")
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
	if readonlyRecoveryHook != nil {
		readonlyRecoveryHook()
	}

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
