package detect

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/hostmetrics"
	"github.com/wiebe-xyz/bugbarn/internal/secnorm"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

// MaxGroups caps the security groups tracked at once (see lru).
const MaxGroups = 50_000

// forgetHostAfter drops a silent host from heartbeat tracking, so a retired
// machine fires once and is then forgotten.
const forgetHostAfter = 7 * 24 * time.Hour

// Detection is one rule hit, handed to the emitter.
type Detection struct {
	Rule  Rule
	Group []KV // GroupBy fields in rule order; host and metric for metric rules
	At    time.Time
	Count int     // records in the window (security)
	Value float64 // series value (metrics) or seconds silent (heartbeat)
	Limit float64 // effective threshold
	// Sample is the message of the record that tipped the count.
	Sample string
}

// KV is one group field.
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GroupKey is the stable identity of the detection's group.
func (d Detection) GroupKey() string {
	parts := make([]string, len(d.Group))
	for i, kv := range d.Group {
		parts[i] = kv.Key + "=" + kv.Value
	}
	return strings.Join(parts, ",")
}

var detectionCounter metric.Int64Counter

func init() {
	detectionCounter, _ = tracing.Meter().Int64Counter(
		"bugbarn.detect.detections",
		metric.WithDescription("Detections fired, by rule and outcome (queued, dropped)."),
		metric.WithUnit("{detection}"),
	)
}

// Engine evaluates rules against telemetry. It implements telemetry.Observer
// and is safe for concurrent use.
type Engine struct {
	mu      sync.Mutex
	rules   []Rule
	groups  *lru
	series  map[string]*seriesState // rule\x00host\x00metric
	hosts   map[string]*hostState
	out     chan Detection
	log     *slog.Logger
	now     func() time.Time
	started time.Time
	dropped int
}

type seriesState struct {
	since     time.Time // first sample past the threshold; zero when below
	lastFired time.Time
}

type hostState struct {
	cores    int
	lastSeen time.Time
	silent   map[string]bool // heartbeat rule IDs that fired for this silence
}

// NewEngine returns an engine with the given rules whose detections go to a
// channel of the given capacity (see Detections).
func NewEngine(rules []Rule, buffer int, log *slog.Logger) *Engine {
	return &Engine{
		rules:   rules,
		groups:  newLRU(MaxGroups),
		series:  map[string]*seriesState{},
		hosts:   map[string]*hostState{},
		out:     make(chan Detection, buffer),
		log:     log.With("component", "detect"),
		now:     time.Now,
		started: time.Now().UTC(),
	}
}

// Detections is the stream the emitter drains.
func (e *Engine) Detections() <-chan Detection { return e.out }

// SetRules swaps the rule set. Window counts and metric state start over, so
// an edited threshold never acts on counts gathered under the old one.
func (e *Engine) SetRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
	e.groups.reset()
	e.series = map[string]*seriesState{}
}

// Rules returns the active rule set.
func (e *Engine) Rules() []Rule {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Rule(nil), e.rules...)
}

// SeedHosts tells the heartbeat about hosts known from before a restart, so a
// host that died while the writer was down still gets reported.
func (e *Engine) SeedHosts(hosts []hostmetrics.HostInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hosts {
		e.touchHost(h)
	}
}

// ObserveSecurity counts each record against every enabled security rule.
func (e *Engine) ObserveSecurity(ctx context.Context, recs []secnorm.Record) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now().UTC()
	for _, r := range e.rules {
		if !r.Enabled || r.Track != TrackSecurity {
			continue
		}
		for i := range recs {
			if matchAll(r.Match, &recs[i]) {
				e.countRecord(ctx, r, &recs[i], now)
			}
		}
	}
}

func matchAll(conds []Cond, rec *secnorm.Record) bool {
	for _, c := range conds {
		v, _ := rec.Field(c.Field)
		if !c.matches(v) {
			return false
		}
	}
	return true
}

func (e *Engine) countRecord(ctx context.Context, r Rule, rec *secnorm.Record, now time.Time) {
	group := make([]KV, len(r.GroupBy))
	var key strings.Builder
	key.WriteString(r.ID)
	for i, f := range r.GroupBy {
		v, _ := rec.Field(f)
		group[i] = KV{Key: f, Value: v}
		key.WriteByte(0)
		key.WriteString(v)
	}
	// A timestamp from the future (clock skew) would push the window ahead
	// and hide later records; cap it at now.
	ts := rec.TS
	if ts.IsZero() || ts.After(now) {
		ts = now
	}
	c := e.groups.get(key.String(), r.Window.D())
	n := c.add(ts)
	if float64(n) < r.Threshold || !cooledDown(c.lastFired, ts, r.Cooldown.D()) {
		return
	}
	c.lastFired = ts
	e.emit(ctx, Detection{Rule: r, Group: group, At: ts, Count: n, Limit: r.Threshold, Sample: rec.Message})
}

func cooledDown(last, at time.Time, cooldown time.Duration) bool {
	return last.IsZero() || at.Sub(last) >= cooldown
}

// ObserveMetrics updates host liveness and evaluates metric rules.
func (e *Engine) ObserveMetrics(ctx context.Context, samples []hostmetrics.Sample, hosts []hostmetrics.HostInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hosts {
		e.touchHost(h)
	}
	for _, r := range e.rules {
		if !r.Enabled || r.Track != TrackMetrics {
			continue
		}
		for _, s := range samples {
			if globMatch(r.Metric, s.Metric) {
				e.evalSample(ctx, r, s)
			}
		}
	}
}

func (e *Engine) touchHost(h hostmetrics.HostInfo) {
	if h.Host == "" {
		return
	}
	st := e.hosts[h.Host]
	if st == nil {
		st = &hostState{silent: map[string]bool{}}
		e.hosts[h.Host] = st
	}
	if h.Cores > 0 {
		st.cores = h.Cores
	}
	if h.LastSeen.After(st.lastSeen) {
		st.lastSeen = h.LastSeen
		clear(st.silent)
	}
}

func (e *Engine) evalSample(ctx context.Context, r Rule, s hostmetrics.Sample) {
	limit, ok := e.limitFor(r, s.Host)
	if !ok {
		return
	}
	key := r.ID + "\x00" + s.Host + "\x00" + s.Metric
	st := e.series[key]
	if st == nil {
		st = &seriesState{}
		e.series[key] = st
	}
	if !beyond(r.Op, s.Value, limit) {
		st.since = time.Time{}
		return
	}
	if st.since.IsZero() || s.TS.Before(st.since) {
		st.since = s.TS
	}
	if s.TS.Sub(st.since) < r.For.D() || !cooledDown(st.lastFired, s.TS, r.Cooldown.D()) {
		return
	}
	st.lastFired = s.TS
	e.emit(ctx, Detection{
		Rule:  r,
		Group: []KV{{Key: "host", Value: s.Host}, {Key: "metric", Value: s.Metric}},
		At:    s.TS, Value: s.Value, Limit: limit,
		Sample: fmt.Sprintf("%s on %s is %.4g (limit %.4g) for %s", s.Metric, s.Host, s.Value, limit,
			s.TS.Sub(st.since).Round(time.Second)),
	})
}

// limitFor is the rule's threshold for host; false when it is per core and
// the host's core count is unknown.
func (e *Engine) limitFor(r Rule, host string) (float64, bool) {
	if !r.PerCore {
		return r.Threshold, true
	}
	h := e.hosts[host]
	if h == nil || h.cores == 0 {
		return 0, false
	}
	return r.Threshold * float64(h.cores), true
}

func beyond(op string, v, limit float64) bool {
	return (op == "gt" && v > limit) || (op == "lt" && v < limit)
}

// CheckHeartbeats fires each heartbeat rule once for every host silent longer
// than its window. Call it periodically (RunHeartbeat does).
func (e *Engine) CheckHeartbeats(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now().UTC()
	for host, st := range e.hosts {
		silence := now.Sub(st.lastSeen)
		// Silence counts from engine start at the earliest: after a writer
		// restart the consumer is still draining what the agents sent while
		// it was down, and every host would look silent until it catches up.
		if alive := now.Sub(e.started); silence > alive && silence <= forgetHostAfter {
			silence = alive
		}
		if silence > forgetHostAfter {
			e.forgetHost(host)
			continue
		}
		for _, r := range e.rules {
			if !r.Enabled || r.Track != TrackHeartbeat || silence <= r.Window.D() || st.silent[r.ID] {
				continue
			}
			st.silent[r.ID] = true
			e.emit(ctx, Detection{
				Rule: r, Group: []KV{{Key: "host", Value: host}}, At: now,
				Value: silence.Seconds(), Limit: r.Window.D().Seconds(),
				Sample: fmt.Sprintf("%s last reported %s ago (at %s)", host,
					silence.Round(time.Second), st.lastSeen.Format(time.RFC3339)),
			})
		}
	}
}

func (e *Engine) forgetHost(host string) {
	delete(e.hosts, host)
	for key := range e.series {
		if strings.Contains(key, "\x00"+host+"\x00") {
			delete(e.series, key)
		}
	}
}

// RunHeartbeat checks heartbeats every interval until ctx ends.
func (e *Engine) RunHeartbeat(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.CheckHeartbeats(ctx)
		}
	}
}

// emit queues a detection without blocking ingest. A full queue means the
// emitter is stuck; the detection is lost, which is worth an error.
func (e *Engine) emit(ctx context.Context, d Detection) {
	select {
	case e.out <- d:
		detectionCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("rule", d.Rule.ID), attribute.String("outcome", "queued")))
	default:
		e.dropped++
		detectionCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("rule", d.Rule.ID), attribute.String("outcome", "dropped")))
		e.log.Error("detection dropped: emitter queue full", "rule", d.Rule.ID, "group", d.GroupKey(), "dropped_total", e.dropped)
	}
}
