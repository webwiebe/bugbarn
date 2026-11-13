package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points every test at a config file that does not exist. runCommand
// loads the user's config and starts error reporting with the key in it, so a
// test that makes a command fail would otherwise send that error to the real
// BugBarn (project bb-cli, issue BC-2 "boom", 6 events from test runs).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bb-test-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("BB_CONFIG", filepath.Join(dir, "cli.json"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestTestsNeverReadTheUserConfig(t *testing.T) {
	p := configPath()
	if !strings.HasPrefix(p, os.TempDir()) {
		t.Fatalf("configPath() = %q, outside the temp dir: tests could report to a real BugBarn", p)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig found a config during tests")
	}
}
