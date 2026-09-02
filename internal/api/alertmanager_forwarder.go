package api

import (
	"context"
	"encoding/base64"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/queue"
)

// publishAlertmanager expands a webhook envelope only when a reader drains it
// into Redis. HTTP forwarding preserves the envelope for the writer to expand.
func (s *SpoolForwarder) publishAlertmanager(ctx context.Context, rec spooledRequest) error {
	body, err := base64.StdEncoding.DecodeString(rec.BodyBase64)
	if err != nil {
		return nil // corrupt local spool record; dropping avoids blocking all ingest
	}
	payloads, err := ingest.AlertmanagerEvents(body)
	if err != nil {
		return nil // malformed input is permanently rejected by the writer
	}
	items := make([]queue.Item, 0, len(payloads))
	for _, payload := range payloads {
		items = append(items, queue.Item{
			Kind: queue.KindEvent, ReceivedAt: rec.ReceivedAt,
			ContentType: "application/json", ProjectSlug: rec.Headers[projectHeader],
			BodyBase64: base64.StdEncoding.EncodeToString(payload),
		})
	}
	err = s.queue.Publish(ctx, items)
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	produceCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	return err
}
