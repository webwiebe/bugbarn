// Package logtest records slog output so tests can assert which records a
// component wrote. Services share one logging rule (ERROR only when something
// was lost, because internal/selflog files every ERROR record as an issue
// against ourselves), and the tests for that rule count ERROR records.
package logtest

import (
	"context"
	"log/slog"
	"sync"
)

// Recorder collects every record written through the logger it returns.
type Recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

// New returns a logger that writes into the recorder at every level.
func New() (*slog.Logger, *Recorder) {
	r := &Recorder{}
	return slog.New(&handler{rec: r}), r
}

// Errors returns the records written at ERROR or above.
func (r *Recorder) Errors() []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []slog.Record
	for _, rec := range r.records {
		if rec.Level >= slog.LevelError {
			out = append(out, rec)
		}
	}
	return out
}

type handler struct {
	rec   *Recorder
	attrs []slog.Attr
}

func (h *handler) Enabled(context.Context, slog.Level) bool { return true }

func (h *handler) Handle(_ context.Context, rec slog.Record) error {
	rec = rec.Clone()
	rec.AddAttrs(h.attrs...)
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	h.rec.records = append(h.rec.records, rec)
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{rec: h.rec, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *handler) WithGroup(string) slog.Handler { return h }
