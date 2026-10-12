package alert

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

const (
	// defaultMailConcurrency is how many alert emails may hold an SMTP
	// connection at once.
	defaultMailConcurrency = 4
	// defaultMailQueue is how many more may wait for a free slot. Beyond that a
	// send is dropped: a relay that is down or throttling would otherwise
	// collect one goroutine and one connection per alert.
	defaultMailQueue = 64
)

// ErrEmailDropped is returned when an alert email was not sent because the
// send queue was full or the send gave up waiting for a slot.
var ErrEmailDropped = errors.New("alert email dropped")

// emailDroppedCounter counts alert emails that were never handed to the relay.
var emailDroppedCounter metric.Int64Counter

func init() {
	emailDroppedCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.alert.email_dropped",
		metric.WithDescription("Alert emails dropped before sending, by reason (queue_full, wait_aborted)."),
		metric.WithUnit("{email}"),
	)
}

// setMailLimits replaces the concurrency and queue bounds. Call before use.
func (d *Deliverer) setMailLimits(concurrent, queued int) {
	d.mailSlots = make(chan struct{}, concurrent)
	d.mailQueue = int64(queued)
}

// EmailDropped reports how many alert emails were dropped since start.
func (d *Deliverer) EmailDropped() int64 { return d.emailDropped.Load() }

// acquireMailSlot waits for a send slot. It returns a release func, or an
// error wrapping ErrEmailDropped when the queue is full or ctx ends first.
func (d *Deliverer) acquireMailSlot(ctx context.Context) (func(), error) {
	release := func() { <-d.mailSlots }
	select {
	case d.mailSlots <- struct{}{}:
		return release, nil
	default:
	}

	if d.emailWaiting.Add(1) > d.mailQueue {
		d.emailWaiting.Add(-1)
		d.countDropped(ctx, "queue_full")
		return nil, fmt.Errorf("%w: queue full", ErrEmailDropped)
	}
	defer d.emailWaiting.Add(-1)

	select {
	case d.mailSlots <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		d.countDropped(ctx, "wait_aborted")
		return nil, fmt.Errorf("%w: %w", ErrEmailDropped, ctx.Err())
	}
}

func (d *Deliverer) countDropped(ctx context.Context, reason string) {
	d.emailDropped.Add(1)
	emailDroppedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}
