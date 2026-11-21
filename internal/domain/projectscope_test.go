package domain

import (
	"context"
	"testing"
)

func TestIssueProjectFromContext(t *testing.T) {
	if _, ok := IssueProjectFromContext(context.Background()); ok {
		t.Fatal("an empty context named a project")
	}
	if _, ok := IssueProjectFromContext(WithIssueProject(context.Background(), 0)); ok {
		t.Fatal("project 0 counted as a scope")
	}
	id, ok := IssueProjectFromContext(WithIssueProject(context.Background(), 7))
	if !ok || id != 7 {
		t.Fatalf("got %d, %v; want 7, true", id, ok)
	}
}
