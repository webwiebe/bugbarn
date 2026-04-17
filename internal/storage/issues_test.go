package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/event"
)

func TestMuteIssue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		muteMode string
		wantErr  bool
	}{
		{"until_regression", "until_regression", false},
		{"forever", "forever", false},
		{"invalid", "never", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()

			ctx := context.Background()
			pe := processedEventFrom(event.Event{
				ObservedAt: time.Now().UTC(),
				ReceivedAt: time.Now().UTC(),
				Severity:   "ERROR",
				Message:    "mute test error " + tc.name,
				Exception:  event.Exception{Type: "MuteError", Message: "mute test error " + tc.name},
			})
			issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe)
			if err != nil {
				t.Fatalf("persist: %v", err)
			}

			muted, err := store.MuteIssue(ctx, issue.ID, tc.muteMode)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error for invalid mute_mode")
				}
				return
			}
			if err != nil {
				t.Fatalf("MuteIssue: %v", err)
			}
			if muted.Status != "muted" {
				t.Errorf("expected status 'muted', got %q", muted.Status)
			}
			if muted.MuteMode != tc.muteMode {
				t.Errorf("expected mute_mode %q, got %q", tc.muteMode, muted.MuteMode)
			}
		})
	}
}

func TestUnmuteIssue(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	pe := processedEventFrom(event.Event{
		ObservedAt: time.Now().UTC(),
		ReceivedAt: time.Now().UTC(),
		Severity:   "ERROR",
		Message:    "unmute test",
		Exception:  event.Exception{Type: "Error", Message: "unmute test"},
	})

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	if _, err := store.MuteIssue(ctx, issue.ID, "forever"); err != nil {
		t.Fatalf("mute: %v", err)
	}

	unmuted, err := store.UnmuteIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("UnmuteIssue: %v", err)
	}
	if unmuted.Status != "unresolved" {
		t.Errorf("expected status 'unresolved', got %q", unmuted.Status)
	}
	if unmuted.MuteMode != "" {
		t.Errorf("expected empty mute_mode, got %q", unmuted.MuteMode)
	}
}

func TestMuteUntilRegressionTriggersOnNewEvent(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	pe := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "regression mute test",
		Exception:  event.Exception{Type: "RError", Message: "regression mute test"},
	})

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe)
	if err != nil {
		t.Fatalf("persist first: %v", err)
	}

	// Resolve then mute until regression.
	if _, err := store.ResolveIssue(ctx, issue.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := store.MuteIssue(ctx, issue.ID, "until_regression"); err != nil {
		t.Fatalf("mute: %v", err)
	}

	// A new event with same fingerprint should trigger a regression and clear mute.
	pe2 := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 13, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 13, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "regression mute test",
		Exception:  event.Exception{Type: "RError", Message: "regression mute test"},
	})

	regressed, _, isNew, isRegressed, err := store.PersistProcessedEvent(ctx, pe2)
	if err != nil {
		t.Fatalf("persist regression: %v", err)
	}
	if isNew {
		t.Error("second event should not be isNew")
	}
	if !isRegressed {
		t.Error("expected isRegressed=true for until_regression mute")
	}
	if regressed.Status != "regressed" {
		t.Errorf("expected status 'regressed', got %q", regressed.Status)
	}
	if regressed.MuteMode != "" {
		t.Errorf("expected mute_mode cleared, got %q", regressed.MuteMode)
	}
}

func TestMuteForeverPreventsRegression(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	pe := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "forever mute test",
		Exception:  event.Exception{Type: "FError", Message: "forever mute test"},
	})

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe)
	if err != nil {
		t.Fatalf("persist first: %v", err)
	}

	if _, err := store.MuteIssue(ctx, issue.ID, "forever"); err != nil {
		t.Fatalf("mute: %v", err)
	}

	pe2 := processedEventFrom(event.Event{
		ObservedAt: time.Date(2026, 4, 15, 13, 0, 0, 0, time.UTC),
		ReceivedAt: time.Date(2026, 4, 15, 13, 0, 1, 0, time.UTC),
		Severity:   "ERROR",
		Message:    "forever mute test",
		Exception:  event.Exception{Type: "FError", Message: "forever mute test"},
	})

	still, _, _, isRegressed, err := store.PersistProcessedEvent(ctx, pe2)
	if err != nil {
		t.Fatalf("persist second: %v", err)
	}
	if isRegressed {
		t.Error("expected isRegressed=false for forever mute")
	}
	if still.Status != "muted" {
		t.Errorf("expected status still 'muted', got %q", still.Status)
	}
}

func TestHourlyEventCounts(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert two events for the same issue in the current hour.
	now := time.Now().UTC()
	pe1 := processedEventFrom(event.Event{
		ObservedAt: now,
		ReceivedAt: now.Add(time.Second),
		Severity:   "ERROR",
		Message:    "hourly count test",
		Exception:  event.Exception{Type: "HError", Message: "hourly count test"},
	})
	pe2 := processedEventFrom(event.Event{
		ObservedAt: now.Add(time.Minute),
		ReceivedAt: now.Add(time.Minute + time.Second),
		Severity:   "ERROR",
		Message:    "hourly count test",
		Exception:  event.Exception{Type: "HError", Message: "hourly count test"},
	})

	issue, _, _, _, err := store.PersistProcessedEvent(ctx, pe1)
	if err != nil {
		t.Fatalf("persist pe1: %v", err)
	}
	if _, _, _, _, err := store.PersistProcessedEvent(ctx, pe2); err != nil {
		t.Fatalf("persist pe2: %v", err)
	}

	rowID, err := parseID(issueIDPrefix, issue.ID)
	if err != nil {
		t.Fatalf("parseID: %v", err)
	}

	counts, err := store.HourlyEventCounts(ctx, []int64{rowID})
	if err != nil {
		t.Fatalf("HourlyEventCounts: %v", err)
	}

	buckets, ok := counts[rowID]
	if !ok {
		t.Fatalf("no counts returned for issue %d", rowID)
	}

	// Both events are in the current hour → bucket index 23.
	if buckets[23] < 2 {
		t.Errorf("expected at least 2 events in current-hour bucket (index 23), got %d", buckets[23])
	}
}

func TestHourlyEventCountsEmptyInput(t *testing.T) {
	t.Parallel()

	store, err := Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	counts, err := store.HourlyEventCounts(context.Background(), nil)
	if err != nil {
		t.Fatalf("HourlyEventCounts with nil IDs: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("expected empty map for nil input, got %v", counts)
	}
}
