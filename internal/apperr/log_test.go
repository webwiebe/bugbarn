package apperr_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/logtest"
)

func TestIsClientError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"not found":   {apperr.NotFound("x", nil), true},
		"conflict":    {apperr.Conflict("x", nil), true},
		"invalid":     {apperr.InvalidInput("x", nil), true},
		"wrapped":     {fmt.Errorf("ctx: %w", apperr.NotFound("x", nil)), true},
		"canceled":    {context.Canceled, true},
		"deadline":    {context.DeadlineExceeded, true},
		"internal":    {apperr.Internal("x", errors.New("boom")), false},
		"unavailable": {apperr.Unavailable("x", nil), false},
		"plain":       {errors.New("boom"), false},
		"nil":         {nil, false},
	}
	for name, tc := range cases {
		if got := apperr.IsClientError(tc.err); got != tc.want {
			t.Errorf("%s: IsClientError = %v, want %v", name, got, tc.want)
		}
	}
}

func TestLogFailureWritesOneErrorForInternalFailuresOnly(t *testing.T) {
	log, rec := logtest.New()
	ctx := context.Background()

	for _, err := range []error{apperr.NotFound("x", nil), apperr.Conflict("x", nil), apperr.InvalidInput("x", nil), context.Canceled} {
		apperr.LogFailure(ctx, log, "op", err)
	}
	if n := len(rec.Errors()); n != 0 {
		t.Fatalf("client errors wrote %d ERROR records, want 0", n)
	}

	apperr.LogFailure(ctx, log, "op", errors.New("boom"), "slug", "a")
	errs := rec.Errors()
	if len(errs) != 1 {
		t.Fatalf("internal error wrote %d ERROR records, want 1", len(errs))
	}
	got := map[string]any{}
	errs[0].Attrs(func(a slog.Attr) bool { got[a.Key] = a.Value.Any(); return true })
	if got["slug"] != "a" || got["error"] == nil {
		t.Errorf("attrs = %v, want slug and error", got)
	}
}
