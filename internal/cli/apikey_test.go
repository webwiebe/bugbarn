package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

func TestRunAPIKeyValidation(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "bugbarn.db")}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "usage"},
		{"unknown", []string{"nope"}, "unknown apikey subcommand"},
		{"missing name", []string{"create"}, "--name is required"},
		{"bad scope", []string{"create", "--name=x", "--scope=root"}, "--scope must be"},
	}
	for _, c := range cases {
		err := RunAPIKey(cfg, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

func TestRunAPIKeyCreatesKeyAndProject(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "bugbarn.db")}
	if err := RunAPIKey(cfg, []string{"create", "--project=fresh", "--name=ci", "--scope=ingest"}); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestRunAPIKeyFindValidatesThePrefix(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "bugbarn.db")}
	for _, prefix := range []string{"", "603ae", "zzzzzzzz"} {
		err := RunAPIKey(cfg, []string{"find", "--sha256-prefix=" + prefix})
		if err == nil || !strings.Contains(err.Error(), "--sha256-prefix must be") {
			t.Errorf("prefix %q: err = %v, want the prefix rule", prefix, err)
		}
	}
}

func TestWriteAPIKeyMatchesReportsTheKeyWithoutItsHash(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	project, err := store.CreateProject(ctx, "legacy", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	const hash = "603ae7d5aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.CreateAPIKey(ctx, "doc key", project.ID, hash, "ingest"); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := writeAPIKeyMatches(ctx, store, "603ae7d5", &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`name="doc key"`, "project=legacy", "scope=ingest", "last_used=never", "1 key(s) match"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, hash) {
		t.Error("the stored hash was printed")
	}
	out.Reset()
	if err := writeAPIKeyMatches(ctx, store, "ffffff", &out); err != nil || !strings.Contains(out.String(), "0 key(s) match") {
		t.Fatalf("no-match output = %q err = %v", out.String(), err)
	}
}

func TestWriteAPIKeyMatchesShowsLastUseAndSurfacesAListFailure(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "bugbarn.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	project, err := store.CreateProject(ctx, "used", "used")
	if err != nil {
		t.Fatal(err)
	}
	const hash = "abc123def456aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.CreateAPIKey(ctx, "in use", project.ID, hash, "full"); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchAPIKey(ctx, hash); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := writeAPIKeyMatches(ctx, store, "abc123", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "last_used=never") || !strings.Contains(out.String(), "last_used=20") {
		t.Fatalf("output = %q, want the last use time", out.String())
	}
	_ = store.Close()
	if err := writeAPIKeyMatches(ctx, store, "abc123", &out); err == nil || !strings.Contains(err.Error(), "list api keys") {
		t.Fatalf("err = %v, want the list failure", err)
	}
}

func TestRunAPIKeyFindRunsAgainstTheConfiguredDatabase(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "bugbarn.db")}
	if err := RunAPIKey(cfg, []string{"find", "--sha256-prefix=ffffff"}); err != nil {
		t.Fatalf("find: %v", err)
	}
	if err := RunAPIKey(cfg, []string{"find", "--bogus"}); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	cfg.DBPath = t.TempDir()
	if err := RunAPIKey(cfg, []string{"find", "--sha256-prefix=ffffff"}); err == nil {
		t.Fatal("an unopenable database was accepted")
	}
}
