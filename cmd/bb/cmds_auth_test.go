package main

import (
	"os"
	"strings"
	"testing"
)

func readConfigFile(t *testing.T) Config {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return cfg
}

func TestLoginWithAPIKeySavesConfig(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	useConfig(t, Config{}) // an empty config, replaced by the login
	if _, err := os.Stat(configPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath()); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, cmdLogin, "--url", h.URL+"/", "--api-key", h.APIKey, "--project", "default", "--no-telemetry")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"auth_type": "apikey"`) || !strings.Contains(out, "Logged in to "+h.URL) {
		t.Errorf("output = %q", out)
	}
	cfg := readConfigFile(t)
	if cfg.URL != h.URL || cfg.Auth.APIKey != h.APIKey || cfg.Project != "default" {
		t.Errorf("saved config = %+v", cfg)
	}
	if cfg.Telemetry == nil || *cfg.Telemetry {
		t.Error("--no-telemetry was not saved")
	}
	info, err := os.Stat(configPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %v %v, want 0600", info.Mode(), err)
	}
}

func TestLoginWithPasswordSavesSession(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	out, err := runCmd(t, cmdLogin, "--url", h.URL, "--username", harnessUser, "--password", harnessPassword, "--no-telemetry")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	cfg := readConfigFile(t)
	if cfg.Auth.Type != "session" || cfg.Auth.SessionToken == "" || cfg.Auth.CSRFToken == "" {
		t.Fatalf("saved auth = %+v", cfg.Auth)
	}

	// The saved session works for reads and, with its CSRF token, for writes.
	h.seedIssue(t, "alpha failure")
	if _, err := runCmd(t, cmdIssues); err != nil {
		t.Errorf("session read: %v", err)
	}
	if _, err := runCmd(t, cmdResolve, "DEF-1"); err != nil {
		t.Errorf("session write: %v", err)
	}
}

func TestLoginErrors(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no url", []string{"--url", "", "--api-key", "k"}, "--url is required"},
		{"no credentials", []string{"--url", h.URL, "--api-key", "", "--username", ""}, "provide --api-key or --username"},
		{"bad api key", []string{"--url", h.URL, "--api-key", "wrong"}, "authentication failed: HTTP 401"},
		{"bad password", []string{"--url", h.URL, "--username", harnessUser, "--password", "wrong"}, "login failed: HTTP 401"},
		{"unknown flag", []string{"--bogus"}, "not defined"},
		{"unreachable", []string{"--url", "http://127.0.0.1:1", "--api-key", "k"}, "request failed"},
		{"unreachable login", []string{"--url", "http://127.0.0.1:1", "--username", "u", "--password", "p"}, "login request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BUGBARN_URL", "")
			t.Setenv("BUGBARN_API_KEY", "")
			t.Setenv("BUGBARN_USERNAME", "")
			_, err := runCmd(t, cmdLogin, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoginReadsDefaultsFromEnvironment(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	t.Setenv("BUGBARN_URL", h.URL)
	t.Setenv("BUGBARN_API_KEY", h.APIKey)
	if out, err := runCmd(t, cmdLogin); err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if cfg := readConfigFile(t); cfg.URL != h.URL || cfg.Auth.Type != "apikey" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestExpiredSessionReauthenticatesOnce(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	h.seedIssue(t, "alpha failure")
	useConfig(t, Config{URL: h.URL, Auth: AuthConfig{
		Type: "session", Username: harnessUser, Password: harnessPassword,
		SessionToken: "stale", CSRFToken: "stale",
	}})

	out, err := runCmd(t, cmdIssues)
	if err != nil {
		t.Fatalf("issues with a stale session: %v\n%s", err, out)
	}
	cfg := readConfigFile(t)
	if cfg.Auth.SessionToken == "stale" || cfg.Auth.SessionToken == "" {
		t.Errorf("the refreshed session was not saved: %+v", cfg.Auth)
	}
}

func TestExpiredSessionWithWrongPasswordFails(t *testing.T) {
	h := newHarness(t)
	inEmptyDir(t)
	useConfig(t, Config{URL: h.URL, Auth: AuthConfig{
		Type: "session", Username: harnessUser, Password: "changed",
		SessionToken: "stale",
	}})
	if _, err := runCmd(t, cmdIssues); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("err = %v, want HTTP 401", err)
	}
}

func TestConfigLoadErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BB_CONFIG", dir+"/cli.json")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("missing file: %v", err)
	}
	if err := writeFile(dir, "cli.json", "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "corrupt config") {
		t.Errorf("corrupt file: %v", err)
	}
	if err := writeFile(dir, "cli.json", `{"auth":{"type":"apikey"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "no URL configured") {
		t.Errorf("no url: %v", err)
	}
	if _, err := newClient(); err == nil {
		t.Error("newClient must fail without a usable config")
	}
}

func TestConfigPathHonorsOverrideAndHome(t *testing.T) {
	t.Setenv("BB_CONFIG", "/custom/cli.json")
	if got := configPath(); got != "/custom/cli.json" {
		t.Errorf("override = %q", got)
	}
	t.Setenv("BB_CONFIG", "")
	t.Setenv("HOME", "/home/dev")
	if got := configPath(); got != "/home/dev/.config/bugbarn/cli.json" {
		t.Errorf("default = %q", got)
	}
}

func TestInitTelemetryHonorsOptOutAndMissingKey(t *testing.T) {
	off := false
	// None of these may start reporting: an opt-out, a session login and an
	// empty URL all return before bb.Init.
	initTelemetry(Config{URL: "http://127.0.0.1:1", Telemetry: &off, Auth: AuthConfig{Type: "apikey", APIKey: "k"}})
	initTelemetry(Config{URL: "http://127.0.0.1:1", Auth: AuthConfig{Type: "session"}})
	initTelemetry(Config{Auth: AuthConfig{Type: "apikey", APIKey: "k"}})
	shutdownTelemetry()
	reportError(nil)
}
