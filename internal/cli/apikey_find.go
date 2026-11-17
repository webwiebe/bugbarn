package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

const minHashPrefix = 6

// findAPIKey handles: bugbarn apikey find --sha256-prefix=603ae7d5
//
// It answers "is this leaked key live on this server?" from the hash of the
// leaked value, so the value never has to be sent here. It prints the key's id,
// name, project, scope and use times, and never the stored hash.
func findAPIKey(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("apikey find", flag.ContinueOnError)
	prefix := fs.String("sha256-prefix", "", "leading hex characters of sha256(key), at least 6")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := strings.ToLower(strings.TrimSpace(*prefix))
	if len(p) < minHashPrefix || strings.Trim(p, "0123456789abcdef") != "" {
		return fmt.Errorf("--sha256-prefix must be at least %d hex characters", minHashPrefix)
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return writeAPIKeyMatches(context.Background(), store, p, os.Stdout)
}

func writeAPIKeyMatches(ctx context.Context, store *storage.Store, prefix string, w io.Writer) error {
	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		return fmt.Errorf("list api keys: %w", err)
	}
	matches := 0
	for _, k := range keys {
		if !strings.HasPrefix(k.KeySHA256, prefix) {
			continue
		}
		matches++
		slug := "(none)"
		if k.ProjectID != 0 {
			if project, err := store.ProjectByID(ctx, k.ProjectID); err == nil {
				slug = project.Slug
			}
		}
		used := "never"
		if !k.LastUsedAt.IsZero() {
			used = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "id=%d name=%q project=%s scope=%s created=%s last_used=%s\n",
			k.ID, k.Name, slug, k.Scope, k.CreatedAt.UTC().Format(time.RFC3339), used)
	}
	_, _ = fmt.Fprintf(w, "%d key(s) match\n", matches)
	return nil
}
