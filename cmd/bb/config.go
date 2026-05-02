package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	URL     string     `json:"url"`
	Auth    AuthConfig `json:"auth"`
	Project string     `json:"project,omitempty"`
}

type AuthConfig struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	CSRFToken    string `json:"csrf_token,omitempty"`
}

func configPath() string {
	if p := os.Getenv("BB_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "bb.json"
	}
	return filepath.Join(home, ".config", "bugbarn", "cli.json")
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("not logged in — run: bb login --url URL --api-key KEY")
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("corrupt config: %w", err)
	}
	if cfg.URL == "" {
		return Config{}, fmt.Errorf("no URL configured — run: bb login --url URL --api-key KEY")
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}
