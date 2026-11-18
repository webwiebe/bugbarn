package projects

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestEnvironmentSuffix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"shop-prod":         "prod",
		"shop-production":   "production",
		"shop-staging":      "staging",
		"shop-test":         "test",
		"shop-testing":      "testing",
		"shop-dev":          "dev",
		"shop-e2e":          "e2e",
		"Shop Staging":      "staging",
		"shop_dev":          "dev",
		"SHOP-PRODUCTION":   "production",
		"shop":              "",
		"staging":           "",
		"shop-developer":    "",
		"protest":           "",
		"latest":            "",
		"shop-staging-tool": "",
		"shop-devops":       "",
		"":                  "",
	}
	for in, want := range cases {
		if got := environmentSuffix(in); got != want {
			t.Errorf("environmentSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateRefusesAnEnvironmentName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, slug string }{
		{"Shop Staging", "shop"},
		{"Shop", "shop-staging"},
		{"shop-e2e", "shop-e2e"},
	} {
		repo := &fakeRepo{projects: map[string]domain.Project{}}
		_, err := New(repo, nil).Create(context.Background(), c.name, c.slug)
		if !errors.Is(err, apperr.ErrInvalidInput) {
			t.Fatalf("Create(%q, %q): err = %v, want invalid input", c.name, c.slug, err)
		}
		if !strings.Contains(err.Error(), "environment field") {
			t.Errorf("message %q does not point at the environment field", err)
		}
		if len(repo.projects) != 0 {
			t.Errorf("Create(%q, %q) stored a project", c.name, c.slug)
		}
	}
}
