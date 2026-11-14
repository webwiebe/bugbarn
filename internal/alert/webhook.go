package alert

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// webhookDeliveryCounter counts alert-webhook delivery attempts, built once
// from the global meter (see tracing.Meter) rather than recreated per call.
var webhookDeliveryCounter metric.Int64Counter

func init() {
	webhookDeliveryCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.alert.webhook_delivery",
		metric.WithDescription("Alert webhook delivery attempts, by outcome and attempt number."),
		metric.WithUnit("{attempt}"),
	)
}

func (d *Deliverer) fireWebhook(ctx context.Context, rule Rule, issue domain.Issue, publicURL string) error {
	payload, err := d.buildPayload(rule, issue, publicURL)
	if err != nil {
		return fmt.Errorf("build payload: %w", err)
	}

	host := webhookHost(rule.WebhookURL)
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}

		lastErr = d.postWebhookAttempt(ctx, rule.WebhookURL, host, attempt+1, payload)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// postWebhookAttempt performs a single POST attempt to a webhook URL, wrapped
// in a span recording the target host (not the full URL, which may embed a
// secret token) and attempt number.
func (d *Deliverer) postWebhookAttempt(ctx context.Context, webhookURL, host string, attempt int, payload []byte) error {
	ctx, span := tracing.Tracer().Start(ctx, "alert.WebhookPost")
	defer span.End()
	span.SetAttributes(
		attribute.String("webhook.host", host),
		attribute.Int("attempt", attempt),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		webhookDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("outcome", "error"),
			attribute.Int("attempt", attempt),
		))
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		webhookDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("outcome", "error"),
			attribute.Int("attempt", attempt),
		))
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		webhookDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("outcome", "success"),
			attribute.Int("attempt", attempt),
		))
		return nil
	}
	err = fmt.Errorf("webhook returned status %d", resp.StatusCode)
	span.SetStatus(codes.Error, err.Error())
	webhookDeliveryCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", "error"),
		attribute.Int("attempt", attempt),
	))
	return err
}

// webhookHost extracts just the host (no scheme/path/query, which may carry a
// secret webhook token) from a webhook URL, for safe use as a span attribute.
func webhookHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}
