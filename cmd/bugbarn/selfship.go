package main

import (
	"os"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingest"
	"github.com/wiebe-xyz/bugbarn/internal/selflog"
)

// shipForeignHeaderLines sends the foreign-project-header line (#235, #248) to
// the self-report project's log store, tagged with the pod and role, so a
// reading over days does not depend on one pod's lifetime. It does nothing
// without self-reporting.
func shipForeignHeaderLines(handler *ingest.Handler, cfg config.Config, component string) {
	if cfg.SelfEndpoint == "" || cfg.SelfAPIKey == "" {
		return
	}
	shipper := selflog.NewLogShipper(cfg.SelfEndpoint, cfg.SelfAPIKey, cfg.SelfProject)
	pod, _ := os.Hostname()
	handler.SetForeignHeaderShipper(func(msg string, attrs map[string]any) {
		attrs["pod"], attrs["component"] = pod, component
		shipper.Ship("info", msg, attrs)
	})
}
