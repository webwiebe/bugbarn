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

// revokeAPIKey handles: bugbarn apikey revoke --id=40 --sha256-prefix=603ae7d5
//
// The prefix is required so a typo in the id cannot revoke another key: the
// key with that id must also have a hash starting with it. Run `apikey find`
// first to get the id.
func revokeAPIKey(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("apikey revoke", flag.ContinueOnError)
	id := fs.Int64("id", 0, "id of the key to revoke")
	prefix := fs.String("sha256-prefix", "", "leading hex characters of the key's sha256, at least 6")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := strings.ToLower(strings.TrimSpace(*prefix))
	if *id <= 0 {
		return fmt.Errorf("--id is required")
	}
	if len(p) < minHashPrefix || strings.Trim(p, "0123456789abcdef") != "" {
		return fmt.Errorf("--sha256-prefix must be at least %d hex characters", minHashPrefix)
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return revokeMatchingKey(context.Background(), store, *id, p, os.Stdout)
}

func revokeMatchingKey(ctx context.Context, store *storage.Store, id int64, prefix string, w io.Writer) error {
	keys, err := store.ListAPIKeys(ctx)
	if err != nil {
		return fmt.Errorf("list api keys: %w", err)
	}
	for _, k := range keys {
		if k.ID != id {
			continue
		}
		if !strings.HasPrefix(k.KeySHA256, prefix) {
			return fmt.Errorf("key %d does not match the given sha256 prefix; nothing revoked", id)
		}
		if err := store.DeleteAPIKey(ctx, id); err != nil {
			return fmt.Errorf("revoke api key: %w", err)
		}
		_, _ = fmt.Fprintf(w, "revoked id=%d name=%q scope=%s\n", k.ID, k.Name, k.Scope)
		return nil
	}
	return fmt.Errorf("no key with id %d", id)
}
