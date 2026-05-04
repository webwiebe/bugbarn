package selflog

import (
	"context"
	"log/slog"

	bb "github.com/wiebe-xyz/bugbarn-go"
)

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
		bb.CaptureMessage(msg)
	}
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{inner: h.inner.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}
