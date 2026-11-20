package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

// renumberProjectIssues handles: bugbarn project renumber --slug=brandtrace --environment=e2e
//
// A merge before the fix of #282 left issues of two projects under one issue
// number. This gives the colliding issues that carry the environment (the ones
// the merge moved in) new numbers after the project's counter.
func renumberProjectIssues(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("project renumber", flag.ContinueOnError)
	slug := fs.String("slug", "", "project slug")
	environment := fs.String("environment", "", "environment of the merged-in issues")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*slug) == "" || strings.TrimSpace(*environment) == "" {
		return fmt.Errorf("usage: bugbarn project renumber --slug=SLUG --environment=ENV")
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return renumberIssues(context.Background(), store, *slug, *environment, os.Stdout)
}

func renumberIssues(ctx context.Context, store *storage.Store, slug, environment string, w io.Writer) error {
	changed, err := store.RenumberDuplicateIssues(ctx, slug, environment)
	if err != nil {
		return fmt.Errorf("renumber issues: %w", err)
	}
	for _, c := range changed {
		_, _ = fmt.Fprintf(w, "number %d -> %d\n", c[0], c[1])
	}
	_, _ = fmt.Fprintf(w, "renumbered %d issues in %s\n", len(changed), slug)
	return nil
}
