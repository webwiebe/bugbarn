package alert

import (
	"context"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// fakeSource records calls; the SQL behind it is tested in internal/storage.
type fakeSource struct {
	alerts    []domain.Alert
	firings   map[string]time.Time
	lastFired map[string]time.Time
}

func (f *fakeSource) ListAlertsForProject(_ context.Context, _ int64) ([]domain.Alert, error) {
	return f.alerts, nil
}

func (f *fakeSource) RecordAlertFiring(_ context.Context, alertID, issueID string) error {
	f.firings[alertID+"/"+issueID] = time.Now().UTC()
	return nil
}

func (f *fakeSource) LastAlertFiring(_ context.Context, alertID, issueID string) (time.Time, error) {
	return f.firings[alertID+"/"+issueID], nil
}

func (f *fakeSource) UpdateAlertLastFired(_ context.Context, alertID string, firedAt time.Time) error {
	f.lastFired[alertID] = firedAt
	return nil
}

func newFakeSource(alerts ...domain.Alert) *fakeSource {
	return &fakeSource{alerts: alerts, firings: map[string]time.Time{}, lastFired: map[string]time.Time{}}
}

func TestStoreRepository_ListForProject(t *testing.T) {
	t.Parallel()

	src := newFakeSource(domain.Alert{
		ID:              "alert-1",
		Name:            "Alert One",
		Enabled:         true,
		WebhookURL:      "https://example.com/hook",
		Condition:       "new_issue",
		Threshold:       3,
		CooldownMinutes: 15,
	})
	rules, err := NewStoreRepository(src).ListForProject(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	want := Rule{
		ID:              "alert-1",
		Name:            "Alert One",
		Enabled:         true,
		ProjectID:       7,
		WebhookURL:      "https://example.com/hook",
		Condition:       "new_issue",
		Threshold:       3,
		CooldownMinutes: 15,
	}
	if rules[0] != want {
		t.Errorf("rule:\n got %+v\nwant %+v", rules[0], want)
	}
}

func TestStoreRepository_Firings(t *testing.T) {
	t.Parallel()

	src := newFakeSource()
	repo := NewStoreRepository(src)
	ctx := context.Background()

	if err := repo.RecordFiring(ctx, "alert-1", "issue-1"); err != nil {
		t.Fatalf("RecordFiring: %v", err)
	}
	last, err := repo.LastFiring(ctx, "alert-1", "issue-1")
	if err != nil || last.IsZero() {
		t.Fatalf("LastFiring: got %v, %v", last, err)
	}
	firedAt := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if err := repo.UpdateLastFired(ctx, "alert-1", firedAt); err != nil {
		t.Fatalf("UpdateLastFired: %v", err)
	}
	if got := src.lastFired["alert-1"]; !got.Equal(firedAt) {
		t.Errorf("last fired: got %v want %v", got, firedAt)
	}
}
