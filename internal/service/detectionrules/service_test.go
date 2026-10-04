package detectionrules

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/detect"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

type fakeRepo struct{ rows map[string]string }

func (f *fakeRepo) ListDetectionRules(context.Context) ([]storage.StoredDetectionRule, error) {
	out := make([]storage.StoredDetectionRule, 0, len(f.rows))
	for id, j := range f.rows {
		out = append(out, storage.StoredDetectionRule{ID: id, JSON: j, UpdatedAt: time.Now()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeRepo) UpsertDetectionRule(_ context.Context, id, j string) error {
	f.rows[id] = j
	return nil
}

func (f *fakeRepo) DeleteDetectionRule(_ context.Context, id string) error {
	if _, ok := f.rows[id]; !ok {
		return apperr.NotFound("detection rule not found", sql.ErrNoRows)
	}
	delete(f.rows, id)
	return nil
}

func builtin(t *testing.T, id string) detect.Rule {
	t.Helper()
	for _, r := range detect.Defaults() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no built-in %q", id)
	return detect.Rule{}
}

func find(rules []detect.Rule, id string) (detect.Rule, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return detect.Rule{}, false
}

func TestPutOverrideReloadsAndDeleteRestores(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := New(&fakeRepo{rows: map[string]string{}}, nil)
	var loaded []detect.Rule
	svc.OnChange(func(r []detect.Rule) { loaded = r })

	disk := builtin(t, "disk-full")
	disk.Threshold = 75
	view, err := svc.Put(ctx, "disk-full", disk)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Builtin || !view.Overridden {
		t.Fatalf("view = %+v", view)
	}
	if r, _ := find(loaded, "disk-full"); r.Threshold != 75 {
		t.Fatalf("engine got threshold %v, want 75", r.Threshold)
	}
	if len(loaded) != len(detect.Defaults()) {
		t.Fatalf("override changed the rule count: %d", len(loaded))
	}

	if err := svc.Delete(ctx, "disk-full"); err != nil {
		t.Fatal(err)
	}
	if r, _ := find(loaded, "disk-full"); r.Threshold != builtin(t, "disk-full").Threshold {
		t.Fatalf("delete did not restore the built-in: %v", r.Threshold)
	}
	if err := svc.Delete(ctx, "disk-full"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("deleting a built-in without override = %v, want NotFound", err)
	}
}

func TestPutCustomRuleAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := New(&fakeRepo{rows: map[string]string{}}, nil)
	custom := builtin(t, "load-high")
	custom.ID, custom.Name, custom.Threshold = "", "Load very high", 4
	if _, err := svc.Put(ctx, "load-very-high", custom); err != nil {
		t.Fatal(err)
	}
	views, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := views[len(views)-1]
	if last.ID != "load-very-high" || last.Builtin || last.Overridden || last.UpdatedAt == nil {
		t.Fatalf("custom view = %+v", last)
	}
	if views[0].Overridden || views[0].UpdatedAt != nil {
		t.Fatalf("untouched built-in marked as stored: %+v", views[0])
	}
}

func TestPutRejectsInvalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := &fakeRepo{rows: map[string]string{}}
	svc := New(repo, nil)
	bad := builtin(t, "disk-full")
	bad.Severity = "LOUD"
	if _, err := svc.Put(ctx, "disk-full", bad); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("invalid severity = %v, want InvalidInput", err)
	}
	if _, err := svc.Put(ctx, "other-id", builtin(t, "disk-full")); !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("id mismatch = %v, want InvalidInput", err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("invalid rules were stored: %v", repo.rows)
	}
}

// A stored row that no longer validates is skipped, so the rest of the rule
// set keeps running.
func TestEffectiveSkipsBrokenRows(t *testing.T) {
	t.Parallel()
	svc := New(&fakeRepo{rows: map[string]string{
		"disk-full": `{"name":"broken","track":"nonsense"}`,
		"junk":      `not json`,
	}}, nil)
	rules, err := svc.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != len(detect.Defaults()) {
		t.Fatalf("got %d rules, want the %d built-ins", len(rules), len(detect.Defaults()))
	}
	if r, _ := find(rules, "disk-full"); r.Name != builtin(t, "disk-full").Name {
		t.Fatalf("broken override replaced the built-in: %+v", r)
	}
}
