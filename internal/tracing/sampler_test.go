package tracing

import (
	"context"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// captureProcessor records spans forwarded to it.
type captureProcessor struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (c *captureProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (c *captureProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	c.mu.Lock()
	c.spans = append(c.spans, s)
	c.mu.Unlock()
}
func (c *captureProcessor) Shutdown(context.Context) error   { return nil }
func (c *captureProcessor) ForceFlush(context.Context) error { return nil }

func (c *captureProcessor) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.spans)
}

// fakeSpan satisfies sdktrace.ReadOnlySpan minimally.
type fakeSpan struct {
	sdktrace.ReadOnlySpan
	sc       trace.SpanContext
	parentSC trace.SpanContext
	status   sdktrace.Status
}

func (f fakeSpan) SpanContext() trace.SpanContext { return f.sc }
func (f fakeSpan) Parent() trace.SpanContext      { return f.parentSC }
func (f fakeSpan) Status() sdktrace.Status        { return f.status }

func makeTraceID(b byte) trace.TraceID {
	var id trace.TraceID
	id[0] = b
	return id
}

func makeSpanID(b byte) trace.SpanID {
	var id trace.SpanID
	id[0] = b
	return id
}

func rootSpan(traceID trace.TraceID, spanID trace.SpanID, status codes.Code) fakeSpan {
	return fakeSpan{
		sc:     trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}),
		status: sdktrace.Status{Code: status},
	}
}

func childSpan(traceID trace.TraceID, spanID, parentID trace.SpanID, status codes.Code) fakeSpan {
	return fakeSpan{
		sc:       trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}),
		parentSC: trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: parentID, TraceFlags: trace.FlagsSampled}),
		status:   sdktrace.Status{Code: status},
	}
}

// TestErrorTraceAlwaysExported verifies that a trace with an error child span
// is always forwarded regardless of the ratio.
func TestErrorTraceAlwaysExported(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 0) // ratio=0 → only error traces pass

	traceID := makeTraceID(0x01)
	root := rootSpan(traceID, makeSpanID(0x01), codes.Ok)
	child := childSpan(traceID, makeSpanID(0x02), makeSpanID(0x01), codes.Error)

	ts.OnEnd(child)
	ts.OnEnd(root) // root last → triggers flush

	if got := cap.count(); got != 2 {
		t.Fatalf("expected 2 spans exported, got %d", got)
	}
}

// TestNonErrorTraceDroppedAtRatioZero verifies that a healthy trace is dropped
// when ratio is 0.
func TestNonErrorTraceDroppedAtRatioZero(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 0)

	traceID := makeTraceID(0x01)
	root := rootSpan(traceID, makeSpanID(0x01), codes.Ok)
	child := childSpan(traceID, makeSpanID(0x02), makeSpanID(0x01), codes.Ok)

	ts.OnEnd(child)
	ts.OnEnd(root)

	if got := cap.count(); got != 0 {
		t.Fatalf("expected 0 spans exported, got %d", got)
	}
}

// TestNonErrorTraceKeptAtRatioOne verifies that all traces pass when ratio=1.
func TestNonErrorTraceKeptAtRatioOne(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 1.0)

	traceID := makeTraceID(0x01)
	root := rootSpan(traceID, makeSpanID(0x01), codes.Ok)
	child := childSpan(traceID, makeSpanID(0x02), makeSpanID(0x01), codes.Ok)

	ts.OnEnd(child)
	ts.OnEnd(root)

	if got := cap.count(); got != 2 {
		t.Fatalf("expected 2 spans exported, got %d", got)
	}
}

// TestKeepRatioApproximate verifies that the sampling ratio is approximately
// correct over a large number of traces.
func TestKeepRatioApproximate(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 0.1)

	const n = 10_000
	for i := range n {
		var id trace.TraceID
		// Use coprime multipliers so the most-significant bytes vary uniformly;
		// keep() reads the first 8 bytes as a big-endian uint64.
		id[0] = byte(i * 251)
		id[1] = byte(i * 127)
		id[2] = byte(i * 223)
		id[3] = byte(i * 191)
		id[4] = byte(i >> 8)
		id[5] = byte(i)
		id[6] = byte(i * 13)
		id[7] = byte(i * 17)
		ts.OnEnd(rootSpan(id, makeSpanID(0x01), codes.Ok))
	}

	got := cap.count()
	// Allow ±3% of n around the 10% target.
	const lo, hi = n*7/100, n*13/100
	if got < lo || got > hi {
		t.Fatalf("expected ~%d traces kept (10%%), got %d", n/10, got)
	}
}

// TestRemoteParentFlushes verifies that a span with a remote parent (e.g. an
// HTTP handler span that received a traceparent header) is treated as a local
// root and triggers the flush immediately.
func TestRemoteParentFlushes(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 1.0)

	traceID := makeTraceID(0x01)
	// Simulate a remote parent: valid but IsRemote=true.
	remoteSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     makeSpanID(0xFF),
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	dbSpan := fakeSpan{
		sc:       trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: makeSpanID(0x02), TraceFlags: trace.FlagsSampled}),
		parentSC: trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: makeSpanID(0x01), TraceFlags: trace.FlagsSampled}),
		status:   sdktrace.Status{Code: codes.Ok},
	}
	handlerSpan := fakeSpan{
		sc:       trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: makeSpanID(0x01), TraceFlags: trace.FlagsSampled}),
		parentSC: remoteSC,
		status:   sdktrace.Status{Code: codes.Ok},
	}

	ts.OnEnd(dbSpan)
	ts.OnEnd(handlerSpan) // remote parent → local root → flush

	if got := cap.count(); got != 2 {
		t.Fatalf("expected 2 spans exported, got %d", got)
	}
}

// TestShutdownFlushesErrorTraces verifies that error traces buffered without a
// root span are forwarded on Shutdown.
func TestShutdownFlushesErrorTraces(t *testing.T) {
	cap := &captureProcessor{}
	ts := NewTailSampler(cap, 0)

	traceID := makeTraceID(0xAA)
	// Only add a child error span; no root span arrives.
	ts.OnEnd(childSpan(traceID, makeSpanID(0x01), makeSpanID(0x00), codes.Error))

	_ = ts.Shutdown(context.Background())

	if got := cap.count(); got != 1 {
		t.Fatalf("expected 1 span exported on shutdown, got %d", got)
	}
}
