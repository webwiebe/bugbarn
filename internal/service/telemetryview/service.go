// Package telemetryview is the read side of the infrastructure telemetry
// files: security log search and host metric series for the dashboard.
//
// It works on every role. The writer and the monolith pass the handles they
// write through; reader pods pass telemetrydb.Lazy sources that open the
// files read-only once the writer has created them.
package telemetryview

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/telemetrydb"
	"github.com/wiebe-xyz/bugbarn/internal/tracing"
)

const (
	// DefaultSecurityLimit and MaxSecurityLimit bound one search page.
	DefaultSecurityLimit = 100
	MaxSecurityLimit     = 500
	// DefaultRange is the window used when a request leaves out from.
	DefaultRange = 24 * time.Hour
	// MinuteDataRange is how far back minute samples are kept; a longer
	// series range reads the hourly rollups.
	MinuteDataRange = 7 * 24 * time.Hour
	// MaxSeriesRange is how far back hourly rollups are kept.
	MaxSeriesRange = 90 * 24 * time.Hour
)

// Service answers telemetry reads.
type Service struct {
	security telemetrydb.Source
	metrics  telemetrydb.Source
	logger   *slog.Logger
	now      func() time.Time
}

// New builds a Service. A nil source behaves like a file that did not open.
func New(security, metrics telemetrydb.Source, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if security == nil {
		security = telemetrydb.Fixed(nil)
	}
	if metrics == nil {
		metrics = telemetrydb.Fixed(nil)
	}
	return &Service{security: security, metrics: metrics, logger: logger.With("service", "telemetryview"), now: time.Now}
}

// Window fills in a missing range: to defaults to now and from to DefaultRange
// before to. It rejects a range that ends before it starts.
func (s *Service) Window(from, to time.Time) (time.Time, time.Time, error) {
	if to.IsZero() {
		to = s.now()
	}
	if from.IsZero() {
		from = to.Add(-DefaultRange)
	}
	if from.After(to) {
		return from, to, apperr.InvalidInput("from must not be after to", nil)
	}
	return from, to, nil
}

// SearchSecurity returns one page of security logs, newest first.
func (s *Service) SearchSecurity(ctx context.Context, q telemetrydb.SecurityQuery) ([]telemetrydb.SecurityRow, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.telemetryview.SearchSecurity")
	defer span.End()
	var err error
	if q.From, q.To, err = s.Window(q.From, q.To); err != nil {
		return nil, err
	}
	if q.Limit == 0 {
		q.Limit = DefaultSecurityLimit
	}
	if q.Limit < 1 || q.Limit > MaxSecurityLimit {
		return nil, apperr.InvalidInput("limit must be between 1 and 500", nil)
	}
	if q.Status != 0 && (q.Status < 100 || q.Status > 599) {
		return nil, apperr.InvalidInput("status must be an HTTP status code", nil)
	}
	db, err := s.security.DB(ctx)
	if err != nil {
		return nil, s.fail(ctx, span, "security", err)
	}
	rows, err := db.SearchSecurity(ctx, q)
	if err != nil {
		return nil, s.fail(ctx, span, "search security logs", err)
	}
	return rows, nil
}

// Hosts lists every host that reported metrics, most recently seen first.
func (s *Service) Hosts(ctx context.Context) ([]telemetrydb.Host, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.telemetryview.Hosts")
	defer span.End()
	db, err := s.metrics.DB(ctx)
	if err != nil {
		return nil, s.fail(ctx, span, "metrics", err)
	}
	hosts, err := db.Hosts(ctx)
	if err != nil {
		return nil, s.fail(ctx, span, "list hosts", err)
	}
	return hosts, nil
}

// HostMetrics lists the metric names a host reported in the minute-data
// window.
func (s *Service) HostMetrics(ctx context.Context, host string) ([]string, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.telemetryview.HostMetrics")
	defer span.End()
	if host == "" {
		return nil, apperr.InvalidInput("host is required", nil)
	}
	db, err := s.metrics.DB(ctx)
	if err != nil {
		return nil, s.fail(ctx, span, "metrics", err)
	}
	names, err := db.Metrics(ctx, host, s.now().Add(-MinuteDataRange))
	if err != nil {
		return nil, s.fail(ctx, span, "list host metrics", err)
	}
	return names, nil
}

// Series is one metric of one host over a range.
type Series struct {
	// Hourly is true when Points are hourly rollups (avg as Value).
	Hourly bool
	Points []telemetrydb.Point
}

// Series returns minute samples for a range up to MinuteDataRange long and
// hourly rollups for longer ranges, up to MaxSeriesRange.
func (s *Service) Series(ctx context.Context, host, metric string, from, to time.Time) (Series, error) {
	ctx, span := tracing.Tracer().Start(ctx, "service.telemetryview.Series")
	defer span.End()
	if host == "" || metric == "" {
		return Series{}, apperr.InvalidInput("host and metric are required", nil)
	}
	from, to, err := s.Window(from, to)
	if err != nil {
		return Series{}, err
	}
	if to.Sub(from) > MaxSeriesRange {
		return Series{}, apperr.InvalidInput("range must not exceed 90 days", nil)
	}
	db, err := s.metrics.DB(ctx)
	if err != nil {
		return Series{}, s.fail(ctx, span, "metrics", err)
	}
	hourly := to.Sub(from) > MinuteDataRange
	points, err := db.Series(ctx, host, metric, from, to, hourly)
	if err != nil {
		return Series{}, s.fail(ctx, span, "query series", err)
	}
	return Series{Hourly: hourly, Points: points}, nil
}

// fail turns a missing file into apperr.Unavailable and logs a real failure.
// A missing file is the expected state on a fresh reader and loses nothing,
// so it is not logged at ERROR.
func (s *Service) fail(ctx context.Context, span trace.Span, op string, err error) error {
	switch {
	case errors.Is(err, telemetrydb.ErrNotCreated):
		return apperr.Unavailable(op+" telemetry has not been created yet; the writer creates it on start", err)
	case errors.Is(err, telemetrydb.ErrUnavailable):
		return apperr.Unavailable(op+" telemetry storage is unavailable on this server", err)
	case apperr.IsContextError(err):
		s.logger.InfoContext(ctx, "telemetry read canceled", "op", op)
		return err
	}
	span.SetStatus(codes.Error, err.Error())
	s.logger.ErrorContext(ctx, "telemetry read failed", "op", op, "error", err)
	return err
}
