package telemetry

import (
	"context"

	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

// IngestRecords is the entry point for writer-side pollers (internal/cfpoller)
// that fetch already-normalized security records. The records go to the
// observer first, then they are stored together with the poller's cursor in
// one transaction, so the cursor never moves past rows that failed to land.
//
// At the size cap the cursor still advances and the raw rows are dropped, the
// same outcome as ErrFull on the Vector path: detections already saw them.
// A failed insert is returned and the cursor stays put, so the caller retries
// the same window on its next run.
func (i *Ingester) IngestRecords(ctx context.Context, recs []secnorm.Record, cursorKey, cursorValue string) error {
	if i.security == nil {
		return ErrUnavailable
	}
	if len(recs) > 0 && i.observer != nil {
		i.observer.ObserveSecurity(ctx, recs)
	}
	full := i.security.Full()
	err := i.security.InsertSecurityWithMeta(ctx, recs, i.now().UTC(), cursorKey, cursorValue)
	if err == nil && full {
		count(ctx, KindSecurity, "full", len(recs))
		if len(recs) > 0 {
			i.log.Warn("telemetry file at its size cap; raw rows dropped", "kind", KindSecurity, "rows", len(recs))
		}
		return nil
	}
	return i.store(ctx, KindSecurity, len(recs), err)
}

// Cursor reads a poller cursor stored by IngestRecords; "" when unset.
func (i *Ingester) Cursor(ctx context.Context, key string) (string, error) {
	if i.security == nil {
		return "", ErrUnavailable
	}
	return i.security.Meta(ctx, key)
}
