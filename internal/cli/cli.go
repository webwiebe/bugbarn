package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	funnelbarn "github.com/webwiebe/funnelbarn/sdks/go"

	"github.com/wiebe-xyz/bugbarn/internal/config"
	"github.com/wiebe-xyz/bugbarn/internal/storage"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// funnelbarnProjectName must match cmd/bugbarn's constant of the same name:
// every bugbarn deployment reports its own product events under one fixed
// FunnelBarn project. Duplicated here (rather than imported) because cli.go
// is invoked directly from cmd/bugbarn's argument dispatch, before that
// package's own long-running server init runs — see trackCLIEvent.
const funnelbarnProjectName = "bugbarn"

// trackCLIEvent reports a product event for a one-shot CLI invocation
// (bugbarn user/project/apikey create). These commands run to completion and
// exit within the same call, unlike the long-running server process that
// initializes FunnelBarn once at startup and shuts it down on SIGTERM — so
// each CLI invocation initializes its own short-lived SDK instance and blocks
// briefly on Shutdown to give the queued event a chance to actually reach the
// network before the process exits. Gated on the API key being configured,
// same as the server.
func trackCLIEvent(cfg config.Config, name string, properties map[string]any) {
	if cfg.FunnelBarnAPIKey == "" {
		return
	}
	funnelbarn.Init(funnelbarn.Options{
		APIKey:      cfg.FunnelBarnAPIKey,
		Endpoint:    cfg.FunnelBarnEndpoint,
		ProjectName: funnelbarnProjectName,
	})
	funnelbarn.Track(name, properties)
	_ = funnelbarn.Shutdown(3 * time.Second)
}

// RunUser handles: bugbarn user create --username=X --password=Y
func RunUser(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn user <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("user create", flag.ContinueOnError)
		username := fs.String("username", os.Getenv("BUGBARN_ADMIN_USERNAME"), "username")
		password := fs.String("password", os.Getenv("BUGBARN_ADMIN_PASSWORD"), "plaintext password")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*username = strings.TrimSpace(*username)
		*password = strings.TrimSpace(*password)
		if *username == "" {
			return fmt.Errorf("--username is required")
		}
		if *password == "" {
			return fmt.Errorf("--password is required")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		store, err := storage.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.UpsertUser(context.Background(), *username, string(hash)); err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		trackCLIEvent(cfg, "cli_login", map[string]any{"action": "user_create"})
		fmt.Printf("user %q created/updated\n", *username)
		return nil
	default:
		return fmt.Errorf("unknown user subcommand %q", args[0])
	}
}

// RunProject handles: bugbarn project create --name=X [--slug=Y]
func RunProject(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn project <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("project create", flag.ContinueOnError)
		name := fs.String("name", "", "project display name")
		slug := fs.String("slug", "", "project slug (defaults to slugified name)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		if *slug == "" {
			*slug = toSlug(*name)
		}
		if !slugPattern.MatchString(*slug) {
			return fmt.Errorf("invalid slug %q: must be lowercase alphanumeric with hyphens", *slug)
		}
		store, err := storage.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer store.Close()
		p, err := store.CreateProject(context.Background(), *name, *slug)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		trackCLIEvent(cfg, "cli_login", map[string]any{"action": "project_create", "project_slug": p.Slug})
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"id":   p.ID,
			"name": p.Name,
			"slug": p.Slug,
		})
	default:
		return fmt.Errorf("unknown project subcommand %q", args[0])
	}
}

// RunAPIKey handles: bugbarn apikey create --project=default --name=my-app
func RunAPIKey(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: bugbarn apikey <create>")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("apikey create", flag.ContinueOnError)
		projectSlug := fs.String("project", "default", "project slug")
		name := fs.String("name", "", "key name/label")
		scope := fs.String("scope", storage.APIKeyScopeFull, "key scope: full, ingest, or read")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		if *scope != storage.APIKeyScopeFull && *scope != storage.APIKeyScopeIngest && *scope != storage.APIKeyScopeRead {
			return fmt.Errorf("--scope must be %q, %q, or %q", storage.APIKeyScopeFull, storage.APIKeyScopeIngest, storage.APIKeyScopeRead)
		}
		store, err := storage.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer store.Close()
		ctx := context.Background()
		project, err := store.ProjectBySlug(ctx, *projectSlug)
		if err != nil {
			// Auto-create the project if it doesn't exist yet.
			project, err = store.CreateProject(ctx, *projectSlug, *projectSlug)
			if err != nil {
				return fmt.Errorf("create project %q: %w", *projectSlug, err)
			}
			fmt.Printf("Project %q created automatically.\n", *projectSlug)
		}
		// Generate a 32-byte random key.
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("generate key: %w", err)
		}
		plaintext := hex.EncodeToString(raw[:])
		sum := sha256.Sum256([]byte(plaintext))
		keySHA256 := hex.EncodeToString(sum[:])

		key, err := store.CreateAPIKey(ctx, *name, project.ID, keySHA256, *scope)
		if err != nil {
			return fmt.Errorf("create api key: %w", err)
		}
		trackCLIEvent(cfg, "api_key_created", map[string]any{"scope": key.Scope, "project_slug": project.Slug})
		fmt.Printf("API key created (id=%d, project=%s, name=%s, scope=%s)\n", key.ID, project.Slug, key.Name, key.Scope)
		fmt.Printf("Key (shown once, store securely): %s\n", plaintext)
		return nil
	default:
		return fmt.Errorf("unknown apikey subcommand %q", args[0])
	}
}

// toSlug converts a display name to a URL-safe slug.
func toSlug(name string) string {
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
