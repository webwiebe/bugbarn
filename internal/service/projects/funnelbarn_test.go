package projects

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestUpdateFunnelBarnURLTrimsAndClears(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	svc := New(repo, nil)
	ctx := context.Background()

	const want = "https://funnelbarn.example/p/shop?surface={surface}"
	got, err := svc.UpdateFunnelBarnURL(ctx, "shop", "  "+want+" ")
	if err != nil {
		t.Fatalf("valid link: %v", err)
	}
	if got != want || repo.funnelSlug != "shop" || repo.funnelURL != want {
		t.Fatalf("returned %q, repository got %q for %q", got, repo.funnelURL, repo.funnelSlug)
	}

	if _, err := svc.UpdateFunnelBarnURL(ctx, "shop", "   "); err != nil || repo.funnelURL != "" {
		t.Fatalf("a blank value clears the link: err %v, stored %q", err, repo.funnelURL)
	}
}

func TestUpdateFunnelBarnURLRejectsBadValues(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{funnelURL: "kept"}
	svc := New(repo, nil)

	for name, bad := range map[string]string{
		"scheme":      "ftp://funnelbarn.example/",
		"no scheme":   "funnelbarn.example",
		"credentials": "https://u:p@funnelbarn.example/",
		"no host":     "https:///x",
		"too long":    "https://funnelbarn.example/" + strings.Repeat("a", 2100),
	} {
		_, err := svc.UpdateFunnelBarnURL(context.Background(), "shop", bad)
		if !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("%s: err %v, want invalid input", name, err)
		}
	}
	if repo.funnelURL != "kept" {
		t.Fatalf("a rejected value reached the repository: %q", repo.funnelURL)
	}
}
