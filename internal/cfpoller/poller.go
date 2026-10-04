// Package cfpoller pulls Cloudflare firewall events from the GraphQL
// Analytics API into the security telemetry file. It runs on the writer only
// (and in the single-process monolith), next to the retention sweep.
//
// Each configured zone keeps its own cursor in the telemetrydb meta table,
// written in the same transaction as the rows it covers (see cursor.go for
// the paging scheme). Records go through the telemetry Ingester so the
// detection engine sees them before they are stored.
package cfpoller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
)

const (
	// Lag is how far behind now the poller reads. Cloudflare's analytics
	// pipeline delivers events with a delay; reading closer to now would
	// advance the cursor past events that have not arrived yet.
	Lag = 2 * time.Minute
	// MaxLookback clamps the start of the window after a long outage or on
	// first start. Older events are not fetched.
	MaxLookback = 24 * time.Hour
	// DefaultPageSize is the GraphQL limit per query.
	DefaultPageSize = 1000
	// MaxQueryLimit is the largest limit Cloudflare accepts on
	// firewallEventsAdaptive.
	MaxQueryLimit = 10000
	// maxPagesPerZone bounds one tick per zone; a backlog resumes next tick.
	maxPagesPerZone = 20
	// maxBackoff caps the wait after repeated failures.
	maxBackoff = 15 * time.Minute
)

// Sink stores records with a cursor. *telemetry.Ingester implements it.
type Sink interface {
	IngestRecords(ctx context.Context, recs []secnorm.Record, cursorKey, cursorValue string) error
	Cursor(ctx context.Context, key string) (string, error)
}

// Config configures a Poller.
type Config struct {
	Token    string
	Zones    []string
	Interval time.Duration
	// Endpoint overrides DefaultEndpoint (tests).
	Endpoint string
	// PageSize overrides DefaultPageSize (tests).
	PageSize int
	// HTTPClient overrides the default client with a 30s timeout.
	HTTPClient *http.Client
	// Now overrides time.Now (tests).
	Now func() time.Time
}

// Poller polls Cloudflare for every configured zone. Not safe for concurrent
// use; Run is its only driver.
type Poller struct {
	token    string
	zones    []string
	interval time.Duration
	endpoint string
	pageSize int
	// secondLimit is the limit used to drain one crowded second.
	secondLimit int
	client      *http.Client
	now         func() time.Time
	sink        Sink
	log         *slog.Logger

	failures   int
	retryAfter time.Duration
}

// New builds a Poller over sink.
func New(cfg Config, sink Sink, log *slog.Logger) *Poller {
	p := &Poller{
		token:       cfg.Token,
		zones:       cfg.Zones,
		interval:    cfg.Interval,
		endpoint:    cfg.Endpoint,
		pageSize:    cfg.PageSize,
		secondLimit: MaxQueryLimit,
		client:      cfg.HTTPClient,
		now:         cfg.Now,
		sink:        sink,
		log:         log.With("component", "cfpoller"),
	}
	if p.interval <= 0 {
		p.interval = time.Minute
	}
	if p.endpoint == "" {
		p.endpoint = DefaultEndpoint
	}
	if p.pageSize <= 0 {
		p.pageSize = DefaultPageSize
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: 30 * time.Second}
	}
	if p.now == nil {
		p.now = time.Now
	}
	return p
}

// Run polls immediately and then after every interval until ctx is done.
// After a failure it waits longer (doubling up to maxBackoff, or the
// server's Retry-After when that is longer).
func (p *Poller) Run(ctx context.Context) {
	p.log.Info("cloudflare poller started", "zones", len(p.zones), "interval", p.interval.String())
	for {
		if err := p.Poll(ctx); err != nil && ctx.Err() == nil {
			p.log.Warn("cloudflare poll failed; retrying later", "error", err, "retry_in", p.delay().String())
		}
		t := time.NewTimer(p.delay())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// Poll runs one pass over all zones. On a rate limit it stops early: every
// zone shares the token's budget. It records the outcome for delay.
func (p *Poller) Poll(ctx context.Context) error {
	var firstErr error
	for _, zone := range p.zones {
		err := p.pollZone(ctx, zone)
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		var rl *rateLimitError
		if errors.As(err, &rl) {
			break
		}
	}
	p.record(firstErr)
	return firstErr
}

func (p *Poller) record(err error) {
	if err == nil {
		p.failures, p.retryAfter = 0, 0
		return
	}
	p.failures++
	p.retryAfter = 0
	var rl *rateLimitError
	if errors.As(err, &rl) {
		p.retryAfter = rl.retryAfter
	}
}

// delay is the wait before the next poll.
func (p *Poller) delay() time.Duration {
	if p.failures == 0 {
		return p.interval
	}
	d := p.interval
	for i := 0; i < p.failures && d < maxBackoff; i++ {
		d *= 2
	}
	d = min(d, maxBackoff)
	return max(d, p.retryAfter)
}

// CursorKey is the meta key holding a zone's cursor.
func CursorKey(zone string) string { return "cloudflare.cursor." + zone }

// pollZone reads one zone's window page by page, storing each page with its
// cursor, until the window is exhausted or maxPagesPerZone is reached.
func (p *Poller) pollZone(ctx context.Context, zone string) error {
	now := p.now().UTC()
	until := now.Add(-Lag).Truncate(time.Second)
	cur, err := p.loadCursor(ctx, zone, now)
	if err != nil {
		return err
	}
	for range maxPagesPerZone {
		if cur.TS.After(until) {
			return nil
		}
		nodes, err := p.fetch(ctx, zone, cur.TS, until, p.pageSize)
		if err != nil {
			return err
		}
		next, done, stalled := p.advance(cur, nodes, until)
		if stalled {
			if next, err = p.drainSecond(ctx, zone, cur); err != nil {
				return err
			}
		}
		if err := p.store(ctx, zone, next.fresh, next.next, now); err != nil {
			return err
		}
		cur = next.next
		if done {
			return nil
		}
	}
	return nil
}

// advance filters one page and picks the next cursor. done reports that the
// window up to until has been read completely. stalled reports a full page of
// rows already stored, all in the cursor's second: paging by datetime cannot
// get past it.
func (p *Poller) advance(cur cursor, nodes []map[string]any, until time.Time) (pg page, done, stalled bool) {
	pg = apply(cur, nodes)
	if len(nodes) < p.pageSize {
		// The whole window is read. Move the cursor to until so the next
		// tick starts there; keys of rows at until stay in Seen.
		if pg.next.TS.Before(until) {
			pg.next = cursor{TS: until}
		}
		return pg, true, false
	}
	return pg, false, len(pg.fresh) == 0 && pg.next.TS.Equal(cur.TS)
}

// drainSecond reads the cursor's second alone with the largest limit
// Cloudflare allows, stores what is new and moves the cursor to the next
// second. Only when even that limit fills are events lost.
func (p *Poller) drainSecond(ctx context.Context, zone string, cur cursor) (page, error) {
	nodes, err := p.fetch(ctx, zone, cur.TS, cur.TS, p.secondLimit)
	if err != nil {
		return page{}, err
	}
	pg := apply(cur, nodes)
	if len(nodes) >= p.secondLimit {
		p.log.Error("cloudflare events lost: more events in one second than one query returns",
			"zone", zone, "second", cur.TS.Format(time.RFC3339), "limit", p.secondLimit)
	}
	pg.next = cursor{TS: cur.TS.Add(time.Second)}
	return pg, nil
}

// loadCursor returns the zone's stored cursor, clamped to MaxLookback.
func (p *Poller) loadCursor(ctx context.Context, zone string, now time.Time) (cursor, error) {
	v, err := p.sink.Cursor(ctx, CursorKey(zone))
	if err != nil {
		return cursor{}, err
	}
	floor := now.Add(-MaxLookback).Truncate(time.Second)
	cur, ok := parseCursor(v)
	switch {
	case !ok:
		p.log.Info("cloudflare zone has no cursor; starting from the lookback window",
			"zone", zone, "since", floor.Format(time.RFC3339))
		return cursor{TS: floor}, nil
	case cur.TS.Before(floor):
		p.log.Error("cloudflare events lost: cursor older than the lookback window, skipping ahead",
			"zone", zone, "cursor", cur.TS.Format(time.RFC3339), "since", floor.Format(time.RFC3339))
		return cursor{TS: floor}, nil
	}
	return cur, nil
}

func (p *Poller) store(ctx context.Context, zone string, nodes []map[string]any, next cursor, now time.Time) error {
	recs := make([]secnorm.Record, 0, len(nodes))
	for _, n := range nodes {
		if rec, ok := secnorm.Cloudflare(n, now); ok {
			rec.Host = firstNonEmpty(rec.Host, zone)
			recs = append(recs, rec)
		}
	}
	return p.sink.IngestRecords(ctx, recs, CursorKey(zone), next.encode())
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
