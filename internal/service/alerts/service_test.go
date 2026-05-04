package alerts

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

type fakeRepo struct {
	alerts map[string]domain.Alert
	err    error
}

func (f *fakeRepo) ListAlerts(context.Context) ([]domain.Alert, error) {
	return nil, f.err
}

func (f *fakeRepo) GetAlert(_ context.Context, id string) (domain.Alert, error) {
	if f.err != nil {
		return domain.Alert{}, f.err
	}
	a, ok := f.alerts[id]
	if !ok {
		return domain.Alert{}, apperr.NotFound("alert not found", nil)
	}
	return a, nil
}

func (f *fakeRepo) CreateAlert(_ context.Context, a domain.Alert) (domain.Alert, error) {
	if f.err != nil {
		return domain.Alert{}, f.err
	}
	a.ID = "alert-1"
	f.alerts[a.ID] = a
	return a, nil
}

func (f *fakeRepo) UpdateAlert(_ context.Context, id string, a domain.Alert) (domain.Alert, error) {
	return domain.Alert{}, f.err
}

func (f *fakeRepo) DeleteAlert(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.alerts[id]; !ok {
		return apperr.NotFound("alert not found", nil)
	}
	delete(f.alerts, id)
	return nil
}

func TestCreate_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{alerts: map[string]domain.Alert{}}
	svc := New(repo, nil)

	input := domain.Alert{Name: "High error rate", Enabled: true}
	got, err := svc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID == "" {
		t.Error("expected non-empty ID")
	}
	if got.Name != "High error rate" {
		t.Errorf("Name: got %q, want %q", got.Name, "High error rate")
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{alerts: map[string]domain.Alert{}}
	svc := New(repo, nil)

	_, err := svc.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{alerts: map[string]domain.Alert{
		"alert-1": {ID: "alert-1", Name: "test"},
	}}
	svc := New(repo, nil)

	err := svc.Delete(context.Background(), "alert-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := repo.alerts["alert-1"]; ok {
		t.Error("expected alert to be deleted from repo")
	}
}
