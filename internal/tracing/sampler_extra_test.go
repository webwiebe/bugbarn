package tracing

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type countingProcessor struct {
	captureProcessor
	starts, flushes int
}

func (c *countingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) { c.starts++ }
func (c *countingProcessor) ForceFlush(context.Context) error                { c.flushes++; return nil }

func TestSamplerDelegatesStartAndForceFlush(t *testing.T) {
	inner := &countingProcessor{}
	ts := NewTailSampler(inner, 1)
	defer ts.Shutdown(context.Background())

	ts.OnStart(context.Background(), nil)
	if err := ts.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	if inner.starts != 1 || inner.flushes != 1 {
		t.Fatalf("inner saw %d starts and %d flushes, want 1 and 1", inner.starts, inner.flushes)
	}
}

func TestEvictStaleDropsOnlyOldTraces(t *testing.T) {
	cp := &captureProcessor{}
	ts := NewTailSampler(cp, 1)
	defer ts.Shutdown(context.Background())

	stale, fresh := makeTraceID(0x0a), makeTraceID(0x0b)
	// Child spans with a parent never flush on their own.
	ts.OnEnd(childSpan(stale, makeSpanID(1), makeSpanID(9), codes.Ok))
	ts.OnEnd(childSpan(fresh, makeSpanID(2), makeSpanID(9), codes.Ok))

	ts.mu.Lock()
	ts.traces[stale].lastSeen = time.Now().Add(-2 * ts.ttl)
	ts.mu.Unlock()

	ts.evictStale()

	ts.mu.Lock()
	_, hasStale := ts.traces[stale]
	_, hasFresh := ts.traces[fresh]
	ts.mu.Unlock()
	if hasStale || !hasFresh {
		t.Fatalf("after eviction stale present = %v, fresh present = %v; want false, true", hasStale, hasFresh)
	}
	if cp.count() != 0 {
		t.Fatalf("evicted traces must be dropped, not exported (exported %d spans)", cp.count())
	}
}

func TestExportIgnoresMissingEntry(t *testing.T) {
	cp := &captureProcessor{}
	ts := NewTailSampler(cp, 1)
	defer ts.Shutdown(context.Background())
	ts.flush(makeTraceID(0x7f))
	if cp.count() != 0 {
		t.Fatal("flushing an unknown trace exported spans")
	}
}
