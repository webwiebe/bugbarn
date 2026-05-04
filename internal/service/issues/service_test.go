package issues

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

// fakeRepo implements Repository for testing.
type fakeRepo struct {
	issues map[string]domain.Issue
	events []domain.Event
	err    error
}

func (f *fakeRepo) ListIssues(context.Context) ([]domain.Issue, error) {
	return nil, f.err
}

func (f *fakeRepo) ListIssuesFiltered(context.Context, domain.IssueFilter) ([]domain.Issue, error) {
	return nil, f.err
}

func (f *fakeRepo) GetIssue(_ context.Context, id string) (domain.Issue, error) {
	if f.err != nil {
		return domain.Issue{}, f.err
	}
	iss, ok := f.issues[id]
	if !ok {
		return domain.Issue{}, apperr.NotFound("issue not found", nil)
	}
	return iss, nil
}

func (f *fakeRepo) ResolveIssue(_ context.Context, id string) (domain.Issue, error) {
	return domain.Issue{}, f.err
}

func (f *fakeRepo) ReopenIssue(_ context.Context, id string) (domain.Issue, error) {
	return domain.Issue{}, f.err
}

func (f *fakeRepo) MuteIssue(_ context.Context, id, muteMode string) (domain.Issue, error) {
	if f.err != nil {
		return domain.Issue{}, f.err
	}
	iss, ok := f.issues[id]
	if !ok {
		return domain.Issue{}, apperr.NotFound("issue not found", nil)
	}
	iss.MuteMode = muteMode
	f.issues[id] = iss
	return iss, nil
}

func (f *fakeRepo) UnmuteIssue(_ context.Context, id string) (domain.Issue, error) {
	return domain.Issue{}, f.err
}

func (f *fakeRepo) HourlyEventCounts(context.Context, []int64) (map[int64][24]int, error) {
	return nil, f.err
}

func (f *fakeRepo) IssueRowIDByDisplayID(context.Context, string) (int64, error) {
	return 0, f.err
}

func (f *fakeRepo) ListIssueEvents(_ context.Context, _ string, _ int, _ int64) ([]domain.Event, bool, error) {
	return nil, false, f.err
}

func (f *fakeRepo) GetEvent(context.Context, string) (domain.Event, error) {
	return domain.Event{}, f.err
}

func (f *fakeRepo) ListRecentEvents(_ context.Context, limit int, since time.Time) ([]domain.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

func (f *fakeRepo) ListFacetKeys(_ context.Context, _ int64) ([]string, error) {
	return nil, f.err
}

func (f *fakeRepo) ListFacetValues(_ context.Context, _ int64, _ string) ([]string, error) {
	return nil, f.err
}

func TestGet_Happy(t *testing.T) {
	t.Parallel()

	want := domain.Issue{ID: "iss-1", Title: "NPE in handler"}
	repo := &fakeRepo{issues: map[string]domain.Issue{"iss-1": want}}
	svc := New(repo, nil)

	got, err := svc.Get(context.Background(), "iss-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("ID: got %q, want %q", got.ID, want.ID)
	}
	if got.Title != want.Title {
		t.Errorf("Title: got %q, want %q", got.Title, want.Title)
	}
}

func TestGet_NotFound(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{issues: map[string]domain.Issue{}}
	svc := New(repo, nil)

	_, err := svc.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMute_Happy(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{issues: map[string]domain.Issue{
		"iss-1": {ID: "iss-1", Title: "test"},
	}}
	svc := New(repo, nil)

	got, err := svc.Mute(context.Background(), "iss-1", "until_regression")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MuteMode != "until_regression" {
		t.Errorf("MuteMode: got %q, want %q", got.MuteMode, "until_regression")
	}
}

func TestMute_InvalidInput(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{err: apperr.InvalidInput("bad mute mode", nil)}
	svc := New(repo, nil)

	_, err := svc.Mute(context.Background(), "iss-1", "bogus")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestListLiveEvents_LimitClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     int
		wantClamp int
	}{
		{"zero becomes 50", 0, 50},
		{"negative becomes 50", -1, 50},
		{"over 100 becomes 50", 200, 50},
		{"valid stays", 25, 25},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedLimit int
			repo := &fakeRepo{}
			// Override ListRecentEvents to capture the limit.
			svc := New(&limitCapture{fakeRepo: repo, capturedLimit: &capturedLimit}, nil)

			_, _ = svc.ListLiveEvents(context.Background(), tc.input, time.Now())
			if capturedLimit != tc.wantClamp {
				t.Errorf("limit: got %d, want %d", capturedLimit, tc.wantClamp)
			}
		})
	}
}

// limitCapture wraps fakeRepo to capture the limit passed to ListRecentEvents.
type limitCapture struct {
	*fakeRepo
	capturedLimit *int
}

func (lc *limitCapture) ListRecentEvents(_ context.Context, limit int, _ time.Time) ([]domain.Event, error) {
	*lc.capturedLimit = limit
	return nil, nil
}

func TestListLiveEvents_DefaultSince(t *testing.T) {
	t.Parallel()

	var capturedSince time.Time
	repo := &fakeRepo{}
	svc := New(&sinceCapture{fakeRepo: repo, capturedSince: &capturedSince}, nil)

	before := time.Now().UTC().Add(-15 * time.Minute)
	_, _ = svc.ListLiveEvents(context.Background(), 10, time.Time{})
	after := time.Now().UTC().Add(-15 * time.Minute)

	if capturedSince.Before(before.Add(-time.Second)) || capturedSince.After(after.Add(time.Second)) {
		t.Errorf("expected since ~15min ago, got %v", capturedSince)
	}
}

// sinceCapture wraps fakeRepo to capture the since passed to ListRecentEvents.
type sinceCapture struct {
	*fakeRepo
	capturedSince *time.Time
}

func (sc *sinceCapture) ListRecentEvents(_ context.Context, _ int, since time.Time) ([]domain.Event, error) {
	*sc.capturedSince = since
	return nil, nil
}
