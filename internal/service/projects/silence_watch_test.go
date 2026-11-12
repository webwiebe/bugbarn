package projects

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateSilenceWatchTrimsAndPassesTheValueOn(t *testing.T) {
	t.Parallel()
	repo := &fakeRepo{}
	svc := New(repo, nil)

	got, err := svc.UpdateSilenceWatch(context.Background(), "load-test", "  off ")
	if err != nil || got != "off" {
		t.Fatalf("UpdateSilenceWatch = %q, %v, want off", got, err)
	}
	if repo.silenceSlug != "load-test" || repo.silenceValue != "off" {
		t.Fatalf("repo got %q %q, want load-test off", repo.silenceSlug, repo.silenceValue)
	}
	if got, err := svc.SilenceWatch(context.Background(), "load-test"); err != nil || got != "off" {
		t.Fatalf("SilenceWatch = %q, %v, want off", got, err)
	}
}

func TestUpdateSilenceWatchReturnsTheRepoError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	svc := New(&fakeRepo{err: boom}, nil)
	if _, err := svc.UpdateSilenceWatch(context.Background(), "x", "off"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}
