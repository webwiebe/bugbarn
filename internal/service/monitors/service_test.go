package monitors

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type fakeRepo struct {
	got     storage.MonitorCheckIn
	err     error
	result  *storage.MonitorCheckInResult
	emitted []int64
}

func (f *fakeRepo) ListEnabledMonitors(context.Context) ([]domain.Monitor, error) { return nil, nil }

func (f *fakeRepo) DeleteMonitor(context.Context, int64, string) error { return nil }

func (f *fakeRepo) MarkMonitorEmitted(_ context.Context, id int64, _ time.Time) error {
	f.emitted = append(f.emitted, id)
	return nil
}

func (f *fakeRepo) RecordMonitorCheckIn(_ context.Context, in storage.MonitorCheckIn) (storage.MonitorCheckInResult, error) {
	f.got = in
	if f.err != nil {
		return storage.MonitorCheckInResult{}, f.err
	}
	if f.result != nil {
		return *f.result, nil
	}
	return storage.MonitorCheckInResult{Monitor: domain.Monitor{Slug: in.Slug, State: domain.MonitorStateOK}}, nil
}

type fakeReporter struct {
	errored, recovered int
	err                error
	recoveredCtxErr    error
}

func (r *fakeReporter) Errored(context.Context, domain.Monitor, string) error {
	r.errored++
	return r.err
}

func (r *fakeReporter) Recovered(ctx context.Context, _ domain.Monitor, _ time.Time) error {
	r.recovered++
	r.recoveredCtxErr = ctx.Err()
	return r.err
}

func TestCheckInReportsErrorOnceThenRecovery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{}
	rep := &fakeReporter{}
	s := newService(repo)
	s.SetReporter(rep)
	in := CheckInInput{Slug: "sweep", Status: domain.CheckInError}

	// First error check-in: state error entered now, never emitted.
	repo.result = &storage.MonitorCheckInResult{PreviousState: domain.MonitorStateOK,
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateError, StateSince: now}}
	if _, err := s.CheckIn(context.Background(), 1, in); err != nil {
		t.Fatal(err)
	}
	if rep.errored != 1 || len(repo.emitted) != 1 {
		t.Fatalf("errored %d, emitted %v", rep.errored, repo.emitted)
	}

	// A second error inside 24h of the report is not reported again.
	repo.result = &storage.MonitorCheckInResult{PreviousState: domain.MonitorStateError,
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateError, StateSince: now.Add(-time.Hour), LastEmittedAt: now.Add(-time.Minute)}}
	if _, err := s.CheckIn(context.Background(), 1, in); err != nil {
		t.Fatal(err)
	}
	if rep.errored != 1 {
		t.Fatalf("repeated error reported again: %d", rep.errored)
	}

	// An ok check-in after the error is a recovery.
	repo.result = &storage.MonitorCheckInResult{PreviousState: domain.MonitorStateError,
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateOK}}
	if _, err := s.CheckIn(context.Background(), 1, CheckInInput{Slug: "sweep", Status: domain.CheckInOK}); err != nil {
		t.Fatal(err)
	}
	if rep.recovered != 1 {
		t.Fatalf("recovered %d", rep.recovered)
	}

	// An ok check-in on a healthy monitor reports nothing.
	repo.result = &storage.MonitorCheckInResult{PreviousState: domain.MonitorStateOK,
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateOK}}
	if _, err := s.CheckIn(context.Background(), 1, CheckInInput{Slug: "sweep", Status: domain.CheckInOK}); err != nil {
		t.Fatal(err)
	}
	if rep.recovered != 1 {
		t.Fatalf("healthy check-in reported a recovery: %d", rep.recovered)
	}
}

func TestCheckInSucceedsWhenReportFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepo{result: &storage.MonitorCheckInResult{
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateError, StateSince: now}}}
	s := newService(repo)
	s.SetReporter(&fakeReporter{err: errors.New("boom")})
	if _, err := s.CheckIn(context.Background(), 1, CheckInInput{Slug: "sweep", Status: domain.CheckInError}); err != nil {
		t.Fatal(err)
	}
	if len(repo.emitted) != 0 {
		t.Fatal("failed report was marked as sent")
	}
}

func (f *fakeRepo) GetMonitor(context.Context, int64, string) (domain.Monitor, error) {
	return domain.Monitor{}, nil
}

func (f *fakeRepo) ListMonitors(context.Context, int64) ([]domain.Monitor, error) { return nil, nil }

func newService(repo *fakeRepo) *Service {
	s := New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetClock(func() time.Time { return time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC) })
	return s
}

func d(v time.Duration) *time.Duration { return &v }

func TestCheckInRejectsBadInputBeforeStorage(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	s := newService(repo)
	for name, in := range map[string]CheckInInput{
		"empty slug":        {Slug: "", Status: "ok"},
		"upper case slug":   {Slug: "Sweep", Status: "ok"},
		"long slug":         {Slug: strings.Repeat("a", 65), Status: "ok"},
		"unknown status":    {Slug: "sweep", Status: "late"},
		"interval too low":  {Slug: "sweep", Status: "ok", Interval: d(30 * time.Second)},
		"interval too high": {Slug: "sweep", Status: "ok", Interval: d(32 * 24 * time.Hour)},
		"negative grace":    {Slug: "sweep", Status: "ok", Grace: d(-time.Second)},
		"grace over":        {Slug: "sweep", Status: "ok", Interval: d(time.Hour), Grace: d(2 * time.Hour)},
	} {
		if _, err := s.CheckIn(context.Background(), 1, in); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if repo.got.Slug != "" {
		t.Fatal("invalid input reached storage")
	}
}

func TestCheckInPassesTimeAndCleansMessage(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	s := newService(repo)
	res, err := s.CheckIn(context.Background(), 7, CheckInInput{
		Slug: "sweep", Status: domain.CheckInError, Message: "  " + strings.Repeat("é", 300) + "  ", Interval: d(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.got.ProjectID != 7 || !repo.got.At.Equal(time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("storage input: %+v", repo.got)
	}
	if n := len([]rune(res.Message)); n != MaxMessageLen {
		t.Fatalf("message has %d characters", n)
	}
}

func TestCheckInPassesStorageErrorsThrough(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{err: apperr.NotFound("monitor not found", nil)}
	_, err := newService(repo).CheckIn(context.Background(), 1, CheckInInput{Slug: "sweep", Status: "ok"})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestCheckInLostLogLevel(t *testing.T) {
	t.Parallel()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		level string
	}{
		{"a live request that failed is an error", context.Background(), "ERROR"},
		{"a canceled request is a warning", canceled, "WARN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			s := New(&fakeRepo{err: apperr.Internal("load monitor", context.Canceled)}, slog.New(slog.NewJSONHandler(&buf, nil)))
			if _, err := s.CheckIn(tc.ctx, 1, CheckInInput{Slug: "sweep", Status: "ok"}); err == nil {
				t.Fatal("expected the storage error")
			}
			if !strings.Contains(buf.String(), `"level":"`+tc.level+`"`) || !strings.Contains(buf.String(), "monitor check-in lost") {
				t.Fatalf("want a %s line, got %q", tc.level, buf.String())
			}
		})
	}
}

func TestCheckInValidatesSchedules(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	s := newService(repo)
	ctx := context.Background()

	for name, in := range map[string]CheckInInput{
		"both modes":         {Slug: "job", Status: "ok", Interval: d(time.Hour), Schedule: "0 6 * * *"},
		"six fields":         {Slug: "job", Status: "ok", Schedule: "0 0 6 * * *"},
		"never runs":         {Slug: "job", Status: "ok", Schedule: "0 0 31 2 *"},
		"unknown zone":       {Slug: "job", Status: "ok", Schedule: "0 6 * * *", Timezone: "Mars/Olympus"},
		"zone with interval": {Slug: "job", Status: "ok", Interval: d(time.Hour), Timezone: "Europe/Amsterdam"},
		"grace past a month": {Slug: "job", Status: "ok", Schedule: "0 6 * * *", Grace: d(32 * 24 * time.Hour)},
	} {
		repo.got = storage.MonitorCheckIn{}
		if _, err := s.CheckIn(ctx, 1, in); !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
		if repo.got.Slug != "" {
			t.Errorf("%s reached storage", name)
		}
	}

	in := CheckInInput{Slug: "job", Status: "ok", Schedule: "  0  6 * * *  ", Timezone: "Europe/Amsterdam", Grace: d(10 * time.Minute)}
	if _, err := s.CheckIn(ctx, 1, in); err != nil {
		t.Fatal(err)
	}
	if repo.got.Schedule == nil || *repo.got.Schedule != "0 6 * * *" || *repo.got.Timezone != "Europe/Amsterdam" || repo.got.Interval != nil {
		t.Fatalf("stored input: %+v", repo.got)
	}
}

// A recovery is reported only on the beat that flips the state, so a request
// that is canceled after the check-in is stored must not abort it.
func TestCheckInReportsRecoveryAfterRequestCancel(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{result: &storage.MonitorCheckInResult{PreviousState: domain.MonitorStateError,
		Monitor: domain.Monitor{ID: 3, State: domain.MonitorStateOK}}}
	rep := &fakeReporter{}
	s := newService(repo)
	s.SetReporter(rep)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.CheckIn(ctx, 1, CheckInInput{Slug: "sweep", Status: domain.CheckInOK}); err != nil {
		t.Fatal(err)
	}
	if rep.recovered != 1 {
		t.Fatalf("recovered = %d, want 1", rep.recovered)
	}
	if rep.recoveredCtxErr != nil {
		t.Fatalf("recovery ran with a canceled context: %v", rep.recoveredCtxErr)
	}
}
