package projects

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
	"github.com/wiebe-xyz/bugbarn/internal/domain"
)

func TestMergeIntoPassesTheEnvironment(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{projects: map[string]domain.Project{}}
	svc := New(repo, nil)
	if err := svc.MergeInto(context.Background(), "a", "b", "staging"); err != nil {
		t.Fatal(err)
	}
	if repo.mergeEnv != "staging" {
		t.Errorf("environment = %q, want staging", repo.mergeEnv)
	}
}

func TestMergeIntoRefusesBadEnvironmentNames(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"bad env", "a/b", "x;y", strings.Repeat("a", 65)} {
		repo := &fakeRepo{projects: map[string]domain.Project{}}
		err := New(repo, nil).MergeInto(context.Background(), "a", "b", env)
		if !errors.Is(err, apperr.ErrInvalidInput) {
			t.Errorf("environment %q: err = %v, want invalid input", env, err)
		}
	}
	if !validEnvironmentName("") || !validEnvironmentName("prod-eu.1_b") {
		t.Error("an empty name and a plain name must pass")
	}
}
