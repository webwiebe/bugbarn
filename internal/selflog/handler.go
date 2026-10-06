package selflog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	bb "github.com/wiebe-xyz/bugbarn-go"
)

const captureTimeout = 2 * time.Second

// Handler reports every record at ERROR or above to BugBarn and passes all
// records on to the wrapped handler.
type Handler struct {
	inner slog.Handler
	// attrs are the attributes bound with WithAttrs, keyed with the group
	// prefix that was open when they were bound.
	attrs map[string]any
	// prefix is the dotted group path ("a.b.") opened with WithGroup.
	prefix string
	// capture sends the report; tests replace it.
	capture func(msg string, attrs map[string]any)
}

func NewHandler(inner slog.Handler) *Handler {
	return &Handler{inner: inner, capture: sendToBugBarn}
}

func sendToBugBarn(msg string, attrs map[string]any) {
	bb.CaptureMessage(msg, bb.WithAttributes(attrs))
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		// The message plus the error attr is what BugBarn groups on, so it
		// stays as it was. Every other attribute travels as event attributes:
		// without them a report such as "ingest pipeline unhealthy" carries
		// no reason and no environment.
		msg := r.Message
		attrs := make(map[string]any, len(h.attrs)+r.NumAttrs())
		for k, v := range h.attrs {
			attrs[k] = v
		}
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "error" {
				msg += ": " + a.Value.String()
			}
			flatten(attrs, h.prefix, a)
			return true
		})
		// Fire-and-forget with a hard timeout so a slow or unavailable ingest
		// endpoint never blocks the caller's logging path.
		done := make(chan struct{}, 1)
		go func() {
			h.capture(msg, attrs)
			done <- struct{}{}
		}()
		select {
		case <-done:
		case <-time.After(captureTimeout):
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]any, len(h.attrs)+len(attrs))
	for k, v := range h.attrs {
		bound[k] = v
	}
	for _, a := range attrs {
		flatten(bound, h.prefix, a)
	}
	return &Handler{inner: h.inner.WithAttrs(attrs), attrs: bound, prefix: h.prefix, capture: h.capture}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{inner: h.inner.WithGroup(name), attrs: h.attrs, prefix: h.prefix + name + ".", capture: h.capture}
}

// flatten writes a into dst under prefix+key, expanding groups into dotted
// keys, and converts the value to something that encodes as JSON.
func flatten(dst map[string]any, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, ga := range v.Group() {
			flatten(dst, p, ga)
		}
		return
	}
	if a.Key == "" {
		return
	}
	dst[prefix+a.Key] = jsonValue(v)
}

func jsonValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	}
	x := v.Any()
	switch t := x.(type) {
	case error:
		return t.Error()
	case fmt.Stringer:
		return t.String()
	}
	// Anything that does not encode would make the SDK drop the whole report.
	if _, err := json.Marshal(x); err != nil {
		return fmt.Sprint(x)
	}
	return x
}
