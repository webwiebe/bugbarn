package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

func TestDetectionRulesUpsertListDelete(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	rules := store.Domains().DetectionRules

	if err := rules.UpsertDetectionRule(ctx, "disk-full", `{"id":"disk-full","threshold":80}`); err != nil {
		t.Fatal(err)
	}
	if err := rules.UpsertDetectionRule(ctx, "disk-full", `{"id":"disk-full","threshold":95}`); err != nil {
		t.Fatal(err)
	}
	if err := rules.UpsertDetectionRule(ctx, "custom", `{"id":"custom"}`); err != nil {
		t.Fatal(err)
	}
	got, err := rules.ListDetectionRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "custom" || got[1].JSON != `{"id":"disk-full","threshold":95}` {
		t.Fatalf("list = %+v", got)
	}
	if got[1].UpdatedAt.IsZero() {
		t.Error("updated_at not parsed")
	}

	if err := rules.DeleteDetectionRule(ctx, "custom"); err != nil {
		t.Fatal(err)
	}
	if err := rules.DeleteDetectionRule(ctx, "custom"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("second delete = %v, want NotFound", err)
	}
}
