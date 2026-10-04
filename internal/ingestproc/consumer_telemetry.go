package ingestproc

import (
	"context"
	"encoding/base64"

	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// TelemetryIngester stores security and metrics batches. Satisfied by
// *telemetry.Ingester.
type TelemetryIngester interface {
	Ingest(ctx context.Context, kind string, body []byte) error
}

// SetTelemetry wires the telemetry ingester. Call before Run.
func (c *Consumer) SetTelemetry(t TelemetryIngester) { c.telemetry = t }

// persistTelemetry hands one telemetry item to the ingester. There is no
// retry: the ingester runs detections before it inserts, so a second attempt
// would count the same lines twice, and the raw rows are best-effort anyway.
// The ingester logs its own failures.
func (c *Consumer) persistTelemetry(ctx context.Context, item queue.Item) string {
	if c.telemetry == nil {
		c.logger.Warn("dropping telemetry item: no telemetry ingester configured", "kind", item.Kind)
		return "dropped"
	}
	body, err := base64.StdEncoding.DecodeString(item.BodyBase64)
	if err != nil {
		c.logger.Error("decode telemetry body", "kind", item.Kind, "error", err)
		return "decode_error"
	}
	if err := c.telemetry.Ingest(ctx, item.Kind, body); err != nil {
		return "insert_error"
	}
	return "success"
}
