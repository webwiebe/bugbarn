package logtest

import (
	"context"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestRecorderKeepsOnlyErrorsInErrors(t *testing.T) {
	t.Parallel()
	log, rec := New()
	log.Debug("debug")
	log.Info("info")
	log.Warn("warn")
	log.Error("first", "k", "v")
	log.Error("second")

	errs := rec.Errors()
	if len(errs) != 2 {
		t.Fatalf("Errors() returned %d records, want 2", len(errs))
	}
	if errs[0].Message != "first" || errs[1].Message != "second" {
		t.Fatalf("messages = %q, %q", errs[0].Message, errs[1].Message)
	}
}

func TestRecorderKeepsOnlyWarningsInWarnings(t *testing.T) {
	t.Parallel()
	log, rec := New()
	log.Info("info")
	log.Warn("warn")
	log.Error("error")

	warns := rec.Warnings()
	if len(warns) != 1 || warns[0].Message != "warn" {
		t.Fatalf("Warnings() = %v, want only the WARN record", warns)
	}
}

func TestRecorderCarriesWithAttrs(t *testing.T) {
	t.Parallel()
	log, rec := New()
	log.With("component", "worker").WithGroup("g").Error("boom")

	errs := rec.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() returned %d records, want 1", len(errs))
	}
	found := false
	errs[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "component" && a.Value.String() == "worker" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("record lost the attribute set through With")
	}
}

func TestRecorderEnabledAtEveryLevel(t *testing.T) {
	t.Parallel()
	log, _ := New()
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelError} {
		if !log.Enabled(context.Background(), lvl) {
			t.Errorf("level %v is disabled", lvl)
		}
	}
}

func TestCheckRuleAcceptsAServiceThatFollowsTheRule(t *testing.T) {
	t.Parallel()
	CheckRule(t, []Call{{
		Name: "follows the rule",
		Run: func(log *slog.Logger, err error) {
			apperr.LogFailure(context.Background(), log, "call failed", err)
		},
	}})
}
