package main

import (
	"time"

	bb "github.com/wiebe-xyz/bugbarn-go"
)

func initTelemetry(cfg Config) {
	if cfg.Telemetry != nil && !*cfg.Telemetry {
		return
	}
	endpoint := cfg.URL
	if endpoint == "" {
		return
	}
	var apiKey string
	if cfg.Auth.Type == "apikey" {
		apiKey = cfg.Auth.APIKey
	}
	if apiKey == "" {
		return
	}
	bb.Init(bb.Options{
		APIKey:      apiKey,
		Endpoint:    endpoint,
		ProjectSlug: "bb-cli",
		Release:     Version,
	})
}

func shutdownTelemetry() {
	bb.Shutdown(2 * time.Second)
}

func reportError(err error) {
	if err != nil {
		bb.CaptureError(err)
	}
}
