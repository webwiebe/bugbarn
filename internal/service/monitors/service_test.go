package monitors

import (
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
	got storage.MonitorCheckIn
	err error
}

func (f *fakeRepo) RecordMonitorCheckIn(_ context.Context, in storage.MonitorCheckIn) (storage.MonitorCheckInResult, error) {
	f.got = in
	if f.err != nil {
		return storage.MonitorCheckInResult{}, f.err
	}
	return storage.MonitorCheckInResult{Monitor: domain.Monitor{Slug: in.Slug, State: domain.MonitorStateOK}}, nil
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
