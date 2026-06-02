package tracing

import (
	"context"
	"math"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TailSampler is a SpanProcessor that makes per-trace sampling decisions after
// all spans have ended (tail-based). It wraps an inner processor (typically the
// batch exporter) and only forwards traces that pass the sampling policy:
//
//   - Traces containing at least one error span are always forwarded.
//   - All other traces are forwarded with probability ratio.
//
// Spans are buffered in memory until the root span ends, at which point the
// whole trace is either forwarded or dropped. Traces whose root span never
// arrives are evicted after ttl and dropped (errors in those traces are still
// captured via the selflog handler before they reach the tracing layer).
type TailSampler struct {
	inner sdktrace.SpanProcessor
	ratio float64
	ttl   time.Duration

	mu     sync.Mutex
	traces map[trace.TraceID]*traceEntry
	done   chan struct{}
}

type traceEntry struct {
	spans    []sdktrace.ReadOnlySpan
	hasError bool
	lastSeen time.Time
}

// NewTailSampler wraps inner with tail-based sampling. ratio must be in [0, 1];
// 0.1 keeps 10% of non-error traces.
func NewTailSampler(inner sdktrace.SpanProcessor, ratio float64) *TailSampler {
	s := &TailSampler{
		inner:  inner,
		ratio:  ratio,
		ttl:    30 * time.Second,
		traces: make(map[trace.TraceID]*traceEntry),
		done:   make(chan struct{}),
	}
	go s.janitor()
	return s
}

func (s *TailSampler) OnStart(parent context.Context, span sdktrace.ReadWriteSpan) {
	s.inner.OnStart(parent, span)
}

func (s *TailSampler) OnEnd(span sdktrace.ReadOnlySpan) {
	traceID := span.SpanContext().TraceID()
	// Flush on local root: either a true root (no parent at all) or a span whose
	// parent lives in another process. HTTP handler spans often have a remote
	// parent from a traceparent header sent by the client or SDK; without this,
	// those traces would never flush and get evicted by the janitor instead.
	isRoot := !span.Parent().IsValid() || span.Parent().IsRemote()

	s.mu.Lock()
	entry := s.traces[traceID]
	if entry == nil {
		entry = &traceEntry{}
		s.traces[traceID] = entry
	}
	entry.spans = append(entry.spans, span)
	entry.lastSeen = time.Now()
	if span.Status().Code == codes.Error {
		entry.hasError = true
	}
	s.mu.Unlock()

	if isRoot {
		s.flush(traceID)
	}
}

func (s *TailSampler) Shutdown(ctx context.Context) error {
	close(s.done)
	s.flushAll()
	return s.inner.Shutdown(ctx)
}

func (s *TailSampler) ForceFlush(ctx context.Context) error {
	return s.inner.ForceFlush(ctx)
}

func (s *TailSampler) flush(traceID trace.TraceID) {
	s.mu.Lock()
	entry := s.traces[traceID]
	delete(s.traces, traceID)
	s.mu.Unlock()

	s.export(traceID, entry)
}

func (s *TailSampler) flushAll() {
	s.mu.Lock()
	remaining := s.traces
	s.traces = make(map[trace.TraceID]*traceEntry)
	s.mu.Unlock()

	for id, entry := range remaining {
		s.export(id, entry)
	}
}

func (s *TailSampler) export(traceID trace.TraceID, entry *traceEntry) {
	if entry == nil {
		return
	}
	if entry.hasError || s.keep(traceID) {
		for _, sp := range entry.spans {
			s.inner.OnEnd(sp)
		}
	}
}

// keep returns a deterministic, consistent sampling decision for a trace ID.
// The first 8 bytes of the ID are treated as a big-endian uint64 and compared
// against a threshold derived from ratio. Because trace IDs are random, this
// gives approximately ratio fraction of traces.
func (s *TailSampler) keep(traceID trace.TraceID) bool {
	var n uint64
	for i := range 8 {
		n = n<<8 | uint64(traceID[i])
	}
	return n <= uint64(s.ratio*float64(math.MaxUint64))
}

func (s *TailSampler) janitor() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictStale()
		case <-s.done:
			return
		}
	}
}

func (s *TailSampler) evictStale() {
	cutoff := time.Now().Add(-s.ttl)
	s.mu.Lock()
	for id, entry := range s.traces {
		if entry.lastSeen.Before(cutoff) {
			delete(s.traces, id)
		}
	}
	s.mu.Unlock()
}
