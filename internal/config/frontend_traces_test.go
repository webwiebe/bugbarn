package config

import "testing"

func TestLoadReadsFrontendTraces(t *testing.T) {
	t.Setenv("BUGBARN_FRONTEND_TRACES_URL", "https://spanbarn.example.com")
	t.Setenv("BUGBARN_FRONTEND_TRACES_API_KEY", "project-key")

	cfg := Load()
	if cfg.FrontendTracesURL != "https://spanbarn.example.com" || cfg.FrontendTracesKey != "project-key" {
		t.Fatalf("frontend traces = %q, %q", cfg.FrontendTracesURL, cfg.FrontendTracesKey)
	}
}

func TestFrontendTracesAreOffByDefault(t *testing.T) {
	t.Setenv("BUGBARN_FRONTEND_TRACES_URL", "")
	t.Setenv("BUGBARN_FRONTEND_TRACES_API_KEY", "")

	cfg := Load()
	if cfg.FrontendTracesURL != "" || cfg.FrontendTracesKey != "" {
		t.Fatalf("frontend traces = %q, %q; want both empty", cfg.FrontendTracesURL, cfg.FrontendTracesKey)
	}
}
