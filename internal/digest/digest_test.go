package digest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// fakeStore serves per-project digest data, optionally blocking on named
// projects to simulate a slow query.
type fakeStore struct {
	projects []domain.Project
	// slow maps project slug -> how long WeeklyDigest blocks before returning.
	slow map[string]time.Duration

	mu     sync.Mutex
	called []string
}

func (f *fakeStore) ListProjects(context.Context) ([]domain.Project, error) {
	return f.projects, nil
}

func (f *fakeStore) WeeklyDigest(ctx context.Context, projectID int64, _ time.Time) (domain.DigestData, error) {
	slug := ""
	for _, p := range f.projects {
		if p.ID == projectID {
			slug = p.Slug
		}
	}

	f.mu.Lock()
	f.called = append(f.called, slug)
	f.mu.Unlock()

	if d, ok := f.slow[slug]; ok {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return domain.DigestData{}, ctx.Err()
		}
	}
	return domain.DigestData{TotalEvents: 1}, nil
}

func (f *fakeStore) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.called...)
}

// fakeNotifier records the reports it was asked to deliver and whether its
// context was still usable at delivery time.
type fakeNotifier struct {
	mu       sync.Mutex
	reports  []Report
	ctxErr   error
	sendErr  error
	nameText string
}

func (n *fakeNotifier) Name() string {
	if n.nameText == "" {
		return "fake"
	}
	return n.nameText
}

func (n *fakeNotifier) Send(ctx context.Context, r Report) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reports = append(n.reports, r)
	n.ctxErr = ctx.Err()
	return n.sendErr
}

func (n *fakeNotifier) delivered() []Report {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Report(nil), n.reports...)
}

func makeProjects(n int) []domain.Project {
	ps := make([]domain.Project, 0, n)
	for i := range n {
		ps = append(ps, domain.Project{ID: int64(i + 1), Slug: fmt.Sprintf("proj-%02d", i+1)})
	}
	return ps
}

// A single slow project must not consume the budget of the projects after it.
// This is the regression that made the tail of a 50-project fleet fail every
// week with "context deadline exceeded".
func TestSendSlowProjectDoesNotStarveTheRest(t *testing.T) {
	cfg := Config{GatherBudget: time.Minute, ProjectBudget: 100 * time.Millisecond}
	store := &fakeStore{
		projects: makeProjects(5),
		// Blocks past its own per-project budget, but well inside the gather budget.
		slow: map[string]time.Duration{"proj-02": 2 * time.Second},
	}
	notifier := &fakeNotifier{}

	errs := Send(context.Background(), cfg, store, []Notifier{notifier})

	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error (the slow project), got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "gather proj-02") {
		t.Errorf("error should name the slow project, got %v", errs[0])
	}

	if got, want := len(store.calls()), 5; got != want {
		t.Errorf("all %d projects should be attempted, got %d: %v", want, got, store.calls())
	}

	reports := notifier.delivered()
	if len(reports) != 1 {
		t.Fatalf("want 1 delivered report, got %d", len(reports))
	}
	if got, want := len(reports[0].Projects), 4; got != want {
		t.Errorf("want %d healthy projects in report, got %d", want, got)
	}
	for _, sec := range reports[0].Projects {
		if sec.Project == "proj-02" {
			t.Error("the timed-out project should not appear in the report")
		}
	}
}

// When the overall gather budget runs out, Send reports that once and stops —
// it must not emit an identical deadline error for every remaining project.
func TestSendGatherBudgetExhaustedReportsOnce(t *testing.T) {
	store := &fakeStore{
		projects: makeProjects(20),
		slow:     map[string]time.Duration{"proj-01": time.Minute},
	}

	errs := Send(context.Background(), Config{GatherBudget: 100 * time.Millisecond}, store, nil)

	if len(errs) != 2 {
		t.Fatalf("want 2 errors (slow project + budget exhausted), got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[1].Error(), "gather budget exhausted after 1/20 projects") {
		t.Errorf("want a single budget-exhausted error naming progress, got %v", errs[1])
	}
	if !errors.Is(errs[1], context.DeadlineExceeded) {
		t.Errorf("budget error should wrap DeadlineExceeded, got %v", errs[1])
	}
	if got := len(store.calls()); got != 1 {
		t.Errorf("should stop after the budget blows, but attempted %d projects", got)
	}
}

// Delivery is budgeted independently of gathering, so a run that burned its
// whole gather budget still ships the sections it managed to build.
func TestSendDeliversAfterGatherBudgetExhausted(t *testing.T) {
	store := &fakeStore{
		projects: makeProjects(3),
		slow:     map[string]time.Duration{"proj-02": time.Minute},
	}
	notifier := &fakeNotifier{}

	Send(context.Background(), Config{GatherBudget: 200 * time.Millisecond}, store, []Notifier{notifier})

	reports := notifier.delivered()
	if len(reports) != 1 {
		t.Fatalf("want the partial report delivered, got %d reports", len(reports))
	}
	if notifier.ctxErr != nil {
		t.Errorf("delivery context must be live, got %v", notifier.ctxErr)
	}
	if got, want := len(reports[0].Projects), 1; got != want {
		t.Errorf("want %d gathered project, got %d", want, got)
	}
}

// Every notifier is attempted even when an earlier one fails.
func TestSendNotifierFailuresAreIndependent(t *testing.T) {
	store := &fakeStore{projects: makeProjects(1)}
	bad := &fakeNotifier{nameText: "webhook", sendErr: errors.New("boom")}
	good := &fakeNotifier{nameText: "email"}

	errs := Send(context.Background(), Config{}, store, []Notifier{bad, good})

	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "webhook: boom") {
		t.Errorf("error should name the failing channel, got %v", errs[0])
	}
	if len(good.delivered()) != 1 {
		t.Error("the healthy notifier should still have been attempted")
	}
}

func TestConfigBudgetDefaults(t *testing.T) {
	if got := (Config{}).gatherBudget(); got != DefaultGatherBudget {
		t.Errorf("want default gather %v, got %v", DefaultGatherBudget, got)
	}
	if got := (Config{GatherBudget: 5 * time.Second}).gatherBudget(); got != 5*time.Second {
		t.Errorf("want configured gather 5s, got %v", got)
	}
	if got := (Config{}).projectBudget(); got != DefaultProjectBudget {
		t.Errorf("want default project %v, got %v", DefaultProjectBudget, got)
	}
	if got := (Config{ProjectBudget: 3 * time.Second}).projectBudget(); got != 3*time.Second {
		t.Errorf("want configured project 3s, got %v", got)
	}
}
