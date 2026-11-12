package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/ingestproc"
	"github.com/wiebe-xyz/bugbarn/internal/silence"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// startSilenceRules starts the revised silence rules (bugbarn#252). Writer-only:
// they read the whole database and, in file mode, persist through the same
// pipeline as SDK events. Dry run is the default and files nothing. Every
// finding also goes into the self-report project's logs, so `bb logs` shows it.
func startSilenceRules(ctx context.Context, cfg config.Config, store *storage.Store, proc *ingestproc.Processor, wg *sync.WaitGroup, log *slog.Logger) {
	rulesCfg := silence.DefaultRulesConfig()
	rulesCfg.Mode = silence.ParseMode(cfg.SilenceRules)
	rulesLog := log.With("component", "silence-rules")
	silence.NewRules(rulesCfg, silence.StoreSource{ProjectStore: store.Domains().Projects}, rulesLog).
		WithShipper(silence.NewLogShipper(store, store, cfg.SelfProject, rulesLog)).
		WithFiler(silence.NewReporter(store, internalPersist(proc), cfg.SilenceProject, rulesLog)).
		Start(ctx, wg)
}
