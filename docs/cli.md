# bb — BugBarn CLI

`bb` is a command-line client for querying and managing BugBarn. It outputs JSON by default (ideal for agents and scripts) and includes an interactive TUI for developers.

---

## Install

### Homebrew (macOS)

```sh
brew tap webwiebe/bugbarn
brew install bb
```

### APT (Debian / Ubuntu)

```sh
curl -fsSL https://webwiebe.nl/apt/key.gpg \
  | sudo gpg --dearmor -o /etc/apt/trusted.gpg.d/webwiebe.gpg
echo "deb https://webwiebe.nl/apt/ stable main" \
  | sudo tee /etc/apt/sources.list.d/webwiebe.list
sudo apt-get update && sudo apt-get install bb
```

### Build from source

```sh
go build -o bb ./cmd/bb
```

---

## Authentication

Authenticate once and the credentials are stored in `~/.config/bugbarn/cli.json`.

### API key (recommended for agents)

```sh
bb login --url https://bugbarn.example.com --api-key YOUR_KEY
```

### Username and password

```sh
bb login --url https://bugbarn.example.com --username admin --password yourpass
```

Omit `--password` to be prompted interactively.

### Environment variables

`BUGBARN_URL` and `BUGBARN_API_KEY` are used as defaults for the `--url` and `--api-key` flags.

---

## Commands

### Issues

```sh
bb issues                              # list open issues (JSON)
bb issues --status all                 # all issues regardless of status
bb issues --status resolved            # only resolved issues
bb issues --sort event_count           # sort by event count
bb issues --query "OOM"                # search by title
```

### Issue detail

```sh
bb issue issue-000042                  # full issue detail with representative event
```

### Events

```sh
bb events issue-000042                 # list events for an issue
bb events issue-000042 --limit 100     # up to 100 events
```

### Manage issues

```sh
bb resolve issue-000042                # resolve an issue
bb reopen issue-000042                 # reopen a resolved issue
bb mute issue-000042                   # mute until next regression
bb mute issue-000042 --mode forever    # mute permanently
bb unmute issue-000042                 # unmute
```

### Logs

```sh
bb logs                                # last 50 logs (colored output)
bb logs --limit 200                    # more entries
bb logs --level warn                   # only warnings and above
bb logs --level error --project backend
bb logs --query "failed"               # search log messages
bb logs -f                             # live-tail via SSE
bb logs -f --level error               # stream only errors
bb logs -f --project backend           # stream one project
bb logs --no-color                     # raw JSON for piping
```

### Projects

```sh
bb projects                            # list all projects
bb projects --create "My App"          # create a new project
bb projects --create "My App" --slug my-app
```

### API keys

```sh
bb apikeys                             # list all API keys (no secrets shown)
```

### Interactive TUI

```sh
bb tui                                 # interactive issue browser
bb tui --status all                    # browse all issues
```

The TUI provides keyboard-driven navigation:

| Key | Action |
|---|---|
| `j` / `k` or arrows | Navigate issues |
| `enter` | View issue detail (stack trace, metadata) |
| `r` | Resolve or reopen the selected issue |
| `R` | Refresh the issue list |
| `esc` | Back to list / quit |
| `q` | Quit |

---

## Configuration

Config file: `~/.config/bugbarn/cli.json` (override with `BB_CONFIG` env var).

```json
{
  "url": "https://bugbarn.example.com",
  "auth": {
    "type": "apikey",
    "api_key": "your-key"
  },
  "telemetry": true
}
```

### Telemetry

When authenticated with an API key, `bb` reports its own errors back to BugBarn (project: `bb-cli`). Disable with:

```sh
bb login --url ... --api-key ... --no-telemetry
```

Or set `"telemetry": false` in the config file.

---

## Agent integration

`bb` is designed for AI agents and automation. All commands output structured JSON to stdout, errors go to stderr, and exit codes follow Unix conventions (0 = success, 1 = error).

Typical agent workflow:

```sh
# List open issues
bb issues --status open

# Get details for a specific issue
bb issue issue-000042

# After fixing, mark resolved
bb resolve issue-000042
```
