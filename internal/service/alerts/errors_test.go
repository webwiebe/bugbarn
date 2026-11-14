package alerts

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type stubRepo struct {
	list    []domain.Alert
	updated domain.Alert
	err     error
}

func (s *stubRepo) ListAlerts(context.Context) ([]domain.Alert, error) { return s.list, s.err }
func (s *stubRepo) GetAlert(context.Context, string) (domain.Alert, error) {
	return domain.Alert{}, s.err
}
func (s *stubRepo) CreateAlert(context.Context, domain.Alert) (domain.Alert, error) {
	return domain.Alert{}, s.err
}
func (s *stubRepo) UpdateAlert(_ context.Context, id string, a domain.Alert) (domain.Alert, error) {
	if s.err != nil {
		return domain.Alert{}, s.err
	}
	a.ID = id
	s.updated = a
	return a, nil
}
func (s *stubRepo) DeleteAlert(context.Context, string) error { return s.err }

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestList_ReturnsAlerts(t *testing.T) {
	t.Parallel()
	svc := New(&stubRepo{list: []domain.Alert{{ID: "a"}, {ID: "b"}}}, nil)
	got, err := svc.List(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %v, %v", got, err)
	}
}

// List passes its error through without logging, unlike the other methods.
func TestList_ErrorPassesThroughUnlogged(t *testing.T) {
	t.Parallel()
	logger, buf := captureLogger()
	svc := New(&stubRepo{err: apperr.Internal("db down", nil)}, logger)
	if _, err := svc.List(context.Background()); !errors.Is(err, apperr.ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected log output: %s", buf.String())
	}
}

func TestUpdate_Happy(t *testing.T) {
	t.Parallel()
	repo := &stubRepo{}
	svc := New(repo, nil)
	got, err := svc.Update(context.Background(), "alert-9", domain.Alert{Name: "renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.ID != "alert-9" || repo.updated.Name != "renamed" {
		t.Fatalf("Update result = %+v, repo saw %+v", got, repo.updated)
	}
}

func TestMutationsLogErrorsAndKeepKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []error{apperr.ErrNotFound, apperr.ErrConflict, apperr.ErrInvalidInput, apperr.ErrInternal} {
		cause := wrap(kind)
		cases := map[string]func(*Service) error{
			"get": func(s *Service) error { _, err := s.Get(context.Background(), "x"); return err },
			"create": func(s *Service) error {
				_, err := s.Create(context.Background(), domain.Alert{Name: "n"})
				return err
			},
			"update": func(s *Service) error {
				_, err := s.Update(context.Background(), "x", domain.Alert{})
				return err
			},
			"delete": func(s *Service) error { return s.Delete(context.Background(), "x") },
		}
		for name, call := range cases {
			logger, buf := captureLogger()
			err := call(New(&stubRepo{err: cause}, logger))
			if !errors.Is(err, kind) {
				t.Errorf("%s: err = %v, want kind %v", name, err, kind)
			}
			if !strings.Contains(buf.String(), `"level":"ERROR"`) {
				t.Errorf("%s: failure not logged at error level: %s", name, buf.String())
			}
		}
	}
}

func TestDeleteAndCreateLogInfoOnSuccess(t *testing.T) {
	t.Parallel()
	logger, buf := captureLogger()
	svc := New(&fakeRepo{alerts: map[string]domain.Alert{"alert-1": {ID: "alert-1"}}}, logger)
	if err := svc.Delete(context.Background(), "alert-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !strings.Contains(buf.String(), "alert deleted") || strings.Contains(buf.String(), "ERROR") {
		t.Fatalf("log = %s", buf.String())
	}
}

func wrap(kind error) error {
	switch {
	case errors.Is(kind, apperr.ErrNotFound):
		return apperr.NotFound("nf", nil)
	case errors.Is(kind, apperr.ErrConflict):
		return apperr.Conflict("c", nil)
	case errors.Is(kind, apperr.ErrInvalidInput):
		return apperr.InvalidInput("i", nil)
	}
	return apperr.Internal("x", nil)
}
