package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
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
