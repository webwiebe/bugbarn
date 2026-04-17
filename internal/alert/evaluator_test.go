package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domainevents"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// fakeRepo implements Repository for testing.
type fakeRepo struct {
	rules    []Rule
	firings  map[string]time.Time // key: alertID+"/"+issueID
	recorded []string             // alertID+"/"+issueID pairs
}

func newFakeRepo(rules []Rule) *fakeRepo {
	return &fakeRepo{rules: rules, firings: make(map[string]time.Time)}
}

func (f *fakeRepo) ListForProject(_ context.Context, _ int64) ([]Rule, error) {
	return f.rules, nil
}

func (f *fakeRepo) RecordFiring(_ context.Context, alertID, issueID string) error {
	f.recorded = append(f.recorded, alertID+"/"+issueID)
	return nil
}

func (f *fakeRepo) LastFiring(_ context.Context, alertID, issueID string) (time.Time, error) {
	return f.firings[alertID+"/"+issueID], nil
}

func (f *fakeRepo) UpdateLastFired(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func TestEvaluator_ConditionMatching(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := []Rule{
		{ID: "alert-000001", Name: "New Issues", Enabled: true, Condition: "new_issue", WebhookURL: srv.URL},
		{ID: "alert-000002", Name: "Regressions", Enabled: true, Condition: "regression", WebhookURL: srv.URL},
	}
	repo := newFakeRepo(rules)
	deliverer := NewDeliverer()
	evaluator := NewEvaluator(repo, deliverer, "http://example.com")

	issue := storage.Issue{ID: "issue-000001", Title: "Test"}

	// Only the new_issue rule should fire for IssueCreated.
	evaluator.evaluate(context.Background(), 1, issue, "new_issue")

	// Allow goroutine to complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && fired.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if fired.Load() != 1 {
		t.Errorf("expected 1 firing for new_issue condition, got %d", fired.Load())
	}
}

func TestEvaluator_DisabledRuleSkipped(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := []Rule{
		{ID: "alert-000001", Name: "Disabled", Enabled: false, Condition: "new_issue", WebhookURL: srv.URL},
	}
	repo := newFakeRepo(rules)
	evaluator := NewEvaluator(repo, NewDeliverer(), "http://example.com")

	evaluator.evaluate(context.Background(), 1, storage.Issue{ID: "issue-000001"}, "new_issue")
	time.Sleep(100 * time.Millisecond)

	if fired.Load() != 0 {
		t.Errorf("expected 0 firings for disabled rule, got %d", fired.Load())
	}
}

func TestEvaluator_CooldownPreventsDoubleFire(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := []Rule{
		{
			ID:              "alert-000001",
			Name:            "Cooldown Alert",
			Enabled:         true,
			Condition:       "new_issue",
			WebhookURL:      srv.URL,
			CooldownMinutes: 60,
		},
	}
	repo := newFakeRepo(rules)
	// Pre-seed a recent firing so cooldown is still active.
	repo.firings["alert-000001/issue-000001"] = time.Now().UTC().Add(-5 * time.Minute)

	evaluator := NewEvaluator(repo, NewDeliverer(), "http://example.com")
	evaluator.evaluate(context.Background(), 1, storage.Issue{ID: "issue-000001"}, "new_issue")
	time.Sleep(100 * time.Millisecond)

	if fired.Load() != 0 {
		t.Errorf("expected 0 firings during cooldown, got %d", fired.Load())
	}
}

func TestEvaluator_CooldownExpiredAllowsFire(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := []Rule{
		{
			ID:              "alert-000001",
			Name:            "Cooldown Expired",
			Enabled:         true,
			Condition:       "new_issue",
			WebhookURL:      srv.URL,
			CooldownMinutes: 1,
		},
	}
	repo := newFakeRepo(rules)
	// Pre-seed an old firing (2 minutes ago > 1 minute cooldown).
	repo.firings["alert-000001/issue-000001"] = time.Now().UTC().Add(-2 * time.Minute)

	evaluator := NewEvaluator(repo, NewDeliverer(), "http://example.com")
	evaluator.evaluate(context.Background(), 1, storage.Issue{ID: "issue-000001"}, "new_issue")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && fired.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if fired.Load() != 1 {
		t.Errorf("expected 1 firing after cooldown expired, got %d", fired.Load())
	}
}

func TestEvaluator_HandleEvent(t *testing.T) {
	t.Parallel()

	var fired atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fired.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := []Rule{
		{ID: "alert-000001", Name: "Test", Enabled: true, Condition: "new_issue", WebhookURL: srv.URL},
	}
	evaluator := NewEvaluator(newFakeRepo(rules), NewDeliverer(), "http://example.com")

	// Subscribe via bus and publish.
	var bus domainevents.Bus
	bus.Subscribe(evaluator.HandleEvent)
	bus.Publish(domainevents.IssueCreated{
		Issue:     storage.Issue{ID: "issue-000001", Title: "Test"},
		ProjectID: 1,
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && fired.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if fired.Load() != 1 {
		t.Errorf("expected 1 firing via bus, got %d", fired.Load())
	}
}
