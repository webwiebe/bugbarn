package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// capture returns what f writes to stdout.
func capture(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	f()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func testCfg(t *testing.T) config.Config {
	t.Helper()
	return config.Config{DBPath: filepath.Join(t.TempDir(), "bugbarn.db")}
}

func TestRunUserCreateStoresAUserAndUpdatesIt(t *testing.T) {
	cfg := testCfg(t)
	out := capture(t, func() {
		if err := RunUser(cfg, []string{"create", "--username", " admin ", "--password", "pw1"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `user "admin" created/updated`) {
		t.Errorf("output = %q", out)
	}
	// A second run with another password updates the same user.
	capture(t, func() {
		if err := RunUser(cfg, []string{"create", "--username", "admin", "--password", "pw2"}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunUserReadsEnvironmentDefaults(t *testing.T) {
	cfg := testCfg(t)
	t.Setenv("BUGBARN_ADMIN_USERNAME", "envadmin")
	t.Setenv("BUGBARN_ADMIN_PASSWORD", "envpw")
	out := capture(t, func() {
		if err := RunUser(cfg, []string{"create"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "envadmin") {
		t.Errorf("output = %q", out)
	}
}

func TestRunUserErrors(t *testing.T) {
	t.Setenv("BUGBARN_ADMIN_USERNAME", "")
	t.Setenv("BUGBARN_ADMIN_PASSWORD", "")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "usage: bugbarn user"},
		{"unknown", []string{"delete"}, `unknown user subcommand "delete"`},
		{"no username", []string{"create", "--password", "x"}, "--username is required"},
		{"blank username", []string{"create", "--username", "  ", "--password", "x"}, "--username is required"},
		{"no password", []string{"create", "--username", "a"}, "--password is required"},
		{"bad flag", []string{"create", "--nope"}, "not defined"},
	}
	for _, c := range cases {
		err := RunUser(testCfg(t), c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	bad := config.Config{DBPath: t.TempDir()} // a directory is not a database
	if err := RunUser(bad, []string{"create", "--username", "a", "--password", "b"}); err == nil {
		t.Error("an unusable database path must fail")
	}
}

func TestRunProjectCreatePrintsJSON(t *testing.T) {
	cfg := testCfg(t)
	out := capture(t, func() {
		if err := RunProject(cfg, []string{"create", "--name", "My Cool App!"}); err != nil {
			t.Fatal(err)
		}
	})
	var got struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if got.Slug != "my-cool-app" || got.Name != "My Cool App!" || got.ID == 0 {
		t.Errorf("project = %+v", got)
	}

	capture(t, func() {
		if err := RunProject(cfg, []string{"create", "--name", "Other", "--slug", "custom-slug"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := RunProject(cfg, []string{"create", "--name", "Dup", "--slug", "custom-slug"}); err == nil || !strings.Contains(err.Error(), "create project") {
		t.Errorf("duplicate slug: %v", err)
	}
}

func TestRunProjectErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "usage: bugbarn project"},
		{"unknown", []string{"rm"}, `unknown project subcommand "rm"`},
		{"no name", []string{"create"}, "--name is required"},
		{"bad slug", []string{"create", "--name", "x", "--slug", "Bad Slug"}, "invalid slug"},
		{"bad flag", []string{"create", "--nope"}, "not defined"},
	}
	for _, c := range cases {
		err := RunProject(testCfg(t), c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if err := RunProject(config.Config{DBPath: t.TempDir()}, []string{"create", "--name", "a"}); err == nil {
		t.Error("an unusable database path must fail")
	}
}

func TestRunAPIKeyCreatePrintsKeyOnce(t *testing.T) {
	cfg := testCfg(t)
	out := capture(t, func() {
		if err := RunAPIKey(cfg, []string{"create", "--project", "fresh", "--name", "ci", "--scope", "read"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{`Project "fresh" created automatically.`, "scope=read", "name=ci", "Key (shown once"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The key is printed once as 64 hex characters and stored as a row.
	line := out[strings.Index(out, "Key (shown once"):]
	plaintext := strings.TrimSpace(line[strings.LastIndex(line, ": ")+2:])
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if len(plaintext) != 64 {
		t.Fatalf("key = %q, want 64 hex characters", plaintext)
	}
	keys, err := store.ListAPIKeys(context.Background())
	if err != nil || len(keys) != 1 || keys[0].Name != "ci" {
		t.Errorf("stored keys = %v err=%v", keys, err)
	}

	// An existing project is reused without the "created" notice.
	out = capture(t, func() {
		if err := RunAPIKey(cfg, []string{"create", "--project", "fresh", "--name", "again"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "created automatically") || !strings.Contains(out, "scope=full") {
		t.Errorf("output = %s", out)
	}
}

func TestRunAPIKeyBadDatabase(t *testing.T) {
	err := RunAPIKey(config.Config{DBPath: t.TempDir()}, []string{"create", "--name", "x"})
	if err == nil {
		t.Error("an unusable database path must fail")
	}
}

func TestRunDBSnapshotSettings(t *testing.T) {
	cfg := testCfg(t)
	capture(t, func() {
		if err := RunProject(cfg, []string{"create", "--name", "Keep Me"}); err != nil {
			t.Fatal(err)
		}
	})
	out := filepath.Join(t.TempDir(), "snap.db")
	text := capture(t, func() {
		if err := RunDB(cfg, []string{"snapshot-settings", "--out", out}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(text, "Settings snapshot written to "+out) || !strings.Contains(text, "projects") || !strings.Contains(text, "rows") {
		t.Errorf("output = %q", text)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	snap, err := storage.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if _, err := snap.ProjectBySlug(context.Background(), "keep-me"); err != nil {
		t.Errorf("the snapshot lost the project: %v", err)
	}

	// --src overrides the configured database.
	other := filepath.Join(t.TempDir(), "other.db")
	capture(t, func() {
		if err := RunDB(config.Config{DBPath: "/nonexistent/x.db"}, []string{"snapshot-settings", "--out", other, "--src", cfg.DBPath}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunDBErrors(t *testing.T) {
	cfg := testCfg(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, "usage: bugbarn db"},
		{"unknown", []string{"vacuum"}, "unknown db subcommand: vacuum"},
		{"no out", []string{"snapshot-settings"}, "usage: bugbarn db snapshot-settings"},
		{"bad flag", []string{"snapshot-settings", "--nope"}, "not defined"},
		{"missing source", []string{"snapshot-settings", "--out", filepath.Join(t.TempDir(), "o.db"), "--src", filepath.Join(t.TempDir(), "gone.db")}, "snapshot settings"},
	}
	for _, c := range cases {
		err := RunDB(cfg, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestToSlug(t *testing.T) {
	cases := map[string]string{
		"My App":        "my-app",
		"  Spaces  ":    "spaces",
		"Under_score!!": "under-score",
		"ÄÖ ünï":        "n",
		"":              "",
		"a--b":          "a-b",
	}
	for in, want := range cases {
		if got := toSlug(in); got != want {
			t.Errorf("toSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrackCLIEventIsOffWithoutAKey(t *testing.T) {
	// No API key: returns before initializing the SDK, so nothing is sent.
	trackCLIEvent(config.Config{}, "x", nil)
}
