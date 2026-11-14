package ingesthealth

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

func (m *Monitor) registerGauges() {
	meter := tracing.Meter()
	age, err1 := meter.Float64ObservableGauge(
		"bugbarn.ingest.last_event_age_seconds",
		metric.WithDescription("Seconds since the most recently persisted event."),
	)
	wal, err2 := meter.Int64ObservableGauge(
		"bugbarn.ingest.wal_size_bytes",
		metric.WithDescription("Size of the SQLite write-ahead log file."),
	)
	healthy, err3 := meter.Int64ObservableGauge(
		"bugbarn.ingest.healthy",
		metric.WithDescription("1 when the ingest pipeline is healthy, 0 otherwise."),
	)
	backlog, err4 := meter.Int64ObservableGauge(
		"bugbarn.ingest.spool_backlog_bytes",
		metric.WithDescription("Bytes in the writer spool the worker has not processed yet."),
	)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return
	}
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := m.Snapshot()
		if !s.Sampled {
			return nil
		}
		o.ObserveFloat64(age, s.LastEventAgeSeconds)
		o.ObserveInt64(wal, s.WALSizeBytes)
		if s.SpoolBacklogKnown {
			o.ObserveInt64(backlog, s.SpoolBacklogBytes)
		}
		if s.Healthy {
			o.ObserveInt64(healthy, 1)
		} else {
			o.ObserveInt64(healthy, 0)
		}
		return nil
	}, age, wal, healthy, backlog)
	if err == nil {
		m.reg = reg
	}
}

func (m *Monitor) unregisterGauges() {
	if m.reg != nil {
		_ = m.reg.Unregister()
	}
}
