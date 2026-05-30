package selflog

import (
	"context"
	"log/slog"
	"time"

	bb "github.com/wiebe-xyz/bugbarn-go"
)

const captureTimeout = 2 * time.Second

type Handler struct {
	inner slog.Handler
}

func NewHandler(inner slog.Handler) *Handler {
	return &Handler{inner: inner}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		msg := r.Message
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "error" {
				msg += ": " + a.Value.String()
			}
			return true
		})
		// Fire-and-forget with a hard timeout so a slow or unavailable ingest
		// endpoint never blocks the caller's logging path.
		done := make(chan struct{}, 1)
		go func() {
			bb.CaptureMessage(msg)
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
	return &Handler{inner: h.inner.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}
