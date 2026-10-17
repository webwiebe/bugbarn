package main

import "testing"

func TestResolveVersion(t *testing.T) {
	if got := resolveVersion("main-abc1234", ""); got != "main-abc1234" {
		t.Errorf("without an override the build value stands, got %q", got)
	}
	if got := resolveVersion("main-abc1234", "v1.2.3"); got != "v1.2.3" {
		t.Errorf("the deploy's version must win, got %q", got)
	}
}
