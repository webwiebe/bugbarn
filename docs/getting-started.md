# Getting Started

This guide gets BugBarn running locally and walks you through sending your first error.

---

## Prerequisites

Choose one of:

| Option | Requirement |
|--------|-------------|
| Binary | Go 1.22 or later |
| Docker | Docker Engine with `docker compose` |

---

## 1. Run locally

### Option A — build and run the binary

```sh
go run ./cmd/bugbarn serve
```

BugBarn needs an admin account on first run. Pass the credentials as environment variables:

```sh
BUGBARN_ADMIN_USERNAME=admin \
BUGBARN_ADMIN_PASSWORD_BCRYPT='$2a$12$...' \
BUGBARN_SESSION_SECRET=change-me-in-production \
go run ./cmd/bugbarn serve
```

Generate a bcrypt hash for your chosen password:

```sh
bugbarn user create --username admin --password yourpassword
```

Or use `htpasswd` / any bcrypt tool and set the hash directly in `BUGBARN_ADMIN_PASSWORD_BCRYPT`.

### Option B — Docker Compose

```sh
docker compose up
```

The included `docker-compose.yml` starts BugBarn with sane defaults. Edit the environment section to set your admin credentials and session secret before running in production.

### Default bind address and data paths

| Variable | Default |
|---|---|
| `BUGBARN_ADDR` | `:8080` |
| `BUGBARN_DB_PATH` | `.data/bugbarn.db` |
| `BUGBARN_SPOOL_DIR` | `.data/spool` |

---

## 2. First login

Open [http://localhost:8080](http://localhost:8080) in your browser and sign in with the admin credentials you set above.

---

## 3. Create your first API key

API keys are used by SDKs and the ingest endpoint. Keys can be scoped to `ingest` (write-only) or `full` (read + write).

### Via the CLI

```sh
bugbarn apikey create --project=default --name=my-app --scope=ingest
```

The command prints the raw key. Store it — it is only shown once.

### Via the UI

Go to **Settings → API Keys → Create key**. Select the project and scope, then copy the generated key.

---

## 4. Send your first error

The ingest endpoint is `POST /api/v1/events`. It returns `202 Accepted` immediately; processing is asynchronous.

```sh
curl -s -X POST http://localhost:8080/api/v1/events \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: <your-api-key>" \
  -H "X-BugBarn-Project: default" \
  -d '{
    "exception": {
      "type": "RuntimeError",
      "value": "something went wrong",
      "stacktrace": {
        "frames": [
          {
            "filename": "main.go",
            "function": "main.run",
            "lineno": 42
          }
        ]
      }
    },
    "level": "error",
    "platform": "go"
  }'
```

A `202` response means the event was accepted and queued. Refresh the issues list in the UI and the error will appear within seconds.

---

## 5. SDK quickstart

### Go

```go
import bugbarn "github.com/webwiebe/bugbarn-go"

func main() {
    err := bugbarn.Init(bugbarn.Options{
        DSN:     "http://localhost:8080",
        APIKey:  "your-api-key",
        Project: "default",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer bugbarn.Shutdown()

    if err := doSomething(); err != nil {
        bugbarn.CaptureError(err)
    }
}
```

### TypeScript

```ts
import { init, captureError, shutdown } from "@webwiebe/bugbarn";

init({
  dsn: "http://localhost:8080",
  apiKey: "your-api-key",
  project: "default",
});

try {
  doSomething();
} catch (err) {
  captureError(err);
}

await shutdown();
```

All three SDKs (Go, TypeScript, Python) expose the same four functions: `Init`, `CaptureError`, `CaptureMessage`, `Shutdown`.

---

## 6. Projects

Every event is scoped to a project. Pass the project slug in the `X-BugBarn-Project` HTTP header or configure it in your SDK options. If the project does not exist, BugBarn will create it automatically. The default slug is `default`.

To create a project explicitly via the CLI:

```sh
bugbarn project create --slug my-service --name "My Service"
```

---

## 7. What you will see in the UI

Once events are flowing you have access to:

- **Issues list** — all errors grouped by fingerprint, with event count, first-seen and last-seen timestamps, and status badges (unresolved / resolved / muted / regressed).
- **Event detail** — the full payload for any individual event: exception, stack trace, breadcrumbs, tags, and any extra context. Privacy scrubbing has already run by the time data reaches this view.
- **All-projects view** — a unified feed across every project on the instance. Each item carries a `project_slug` tag so you know where it came from.
- **Log stream** — real-time structured logs delivered over SSE, filterable by level and search string.

---

## Next steps

- [Overview](overview.md) — capabilities, architecture, and what BugBarn is not
- [Operations](operations.md) — Kubernetes, Litestream replication, SMTP for digest emails, production environment variables
