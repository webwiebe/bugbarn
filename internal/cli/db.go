package cli

import (
	"context"
	"flag"
	"fmt"
	"sort"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// RunDB dispatches the `bugbarn db` subcommands.
func RunDB(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn db <snapshot-settings>")
	}
	switch args[0] {
	case "snapshot-settings":
		return runDBSnapshotSettings(cfg, args[1:])
	default:
		return fmt.Errorf("unknown db subcommand: %s", args[0])
	}
}

// runDBSnapshotSettings builds a settings-only snapshot database — projects,
// users, API keys, alert rules, org settings — with every bulk table present but
// empty, for disaster recovery. The output is a complete, ready-to-serve
// bugbarn.db: drop it in as BUGBARN_DB_PATH and start the binary. This is what
// the settings-snapshot CronJob runs.
func runDBSnapshotSettings(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("db snapshot-settings", flag.ContinueOnError)
	out := fs.String("out", "", "Output path for the settings-only snapshot database")
	src := fs.String("src", "", "Source database path (defaults to BUGBARN_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("usage: bugbarn db snapshot-settings --out=PATH [--src=PATH]")
	}
	srcPath := *src
	if srcPath == "" {
		srcPath = cfg.DBPath
	}

	counts, err := storage.SnapshotSettings(context.Background(), srcPath, *out)
	if err != nil {
		return fmt.Errorf("snapshot settings: %w", err)
	}

	fmt.Printf("Settings snapshot written to %s\n", *out)
	tables := make([]string, 0, len(counts))
	for t := range counts {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		fmt.Printf("  %-16s %8d rows\n", t, counts[t])
	}
	return nil
}
