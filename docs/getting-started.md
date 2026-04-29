# Getting Started

This guide gets BugBarn running locally and walks you through sending your first error.

---

## 1. Install BugBarn

### Homebrew (macOS and Linux)

```sh
brew tap webwiebe/bugbarn
brew install bugbarn
```

### APT (Debian / Ubuntu)

```sh
curl -fsSL https://webwiebe.nl/apt/key.gpg \
  | sudo gpg --dearmor -o /etc/apt/trusted.gpg.d/webwiebe.gpg
echo "deb https://webwiebe.nl/apt/ stable main" \
  | sudo tee /etc/apt/sources.list.d/webwiebe.list
sudo apt-get update && sudo apt-get install bugbarn
```

### Docker Compose (pre-built images)

Create a `docker-compose.yml`:

```yaml
name: bugbarn

services:
  service:
    image: ghcr.io/webwiebe/bugbarn/service:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      BUGBARN_ADDR: :8080
      BUGBARN_API_KEY: change-me
      BUGBARN_ADMIN_USERNAME: admin
      BUGBARN_ADMIN_PASSWORD: change-me
      BUGBARN_SESSION_SECRET: change-me-generate-with-openssl-rand-hex-32
      BUGBARN_PUBLIC_URL: http://localhost:8080
      BUGBARN_SPOOL_DIR: /var/lib/bugbarn/spool
      BUGBARN_DB_PATH: /var/lib/bugbarn/bugbarn.db
    volumes:
      - bugbarn-data:/var/lib/bugbarn

  web:
    image: ghcr.io/webwiebe/bugbarn/web:latest
    restart: unless-stopped
    ports:
      - "3000:3000"
    environment:
      BUGBARN_API_URL: http://service:8080
    depends_on:
      - service

volumes:
  bugbarn-data:
```

Then:

```sh
docker compose up -d
```

The UI is at [http://localhost:3000](http://localhost:3000) and the API is at [http://localhost:8080](http://localhost:8080).

> The `docker-compose.yml` in the repository root builds from local source and is intended for development. Use the config above to run pre-built release images.

### Build from source

Requires Go 1.22 or later.

```sh
git clone https://github.com/webwiebe/bugbarn
cd bugbarn
go build -o bugbarn ./cmd/bugbarn
```

Then run with your credentials set:

```sh
BUGBARN_ADMIN_USERNAME=admin \
BUGBARN_ADMIN_PASSWORD=yourpassword \
BUGBARN_SESSION_SECRET=$(openssl rand -hex 32) \
./bugbarn
```

---

## 2. First login

Open [http://localhost:8080](http://localhost:8080) in your browser and sign in with the admin credentials you set above.

---

## 3. Create your first API key

API keys authenticate SDK and ingest calls. Always use `ingest` scope for keys embedded in applications.

### Via the CLI

```sh
bugbarn apikey create --project=default --name=my-app --scope=ingest
```

The command prints the raw key once — copy it immediately.

### Via the UI

Go to **Settings → API Keys → Create key**. Select the project and scope, then copy the generated key.

---

## 4. Send your first error

The ingest endpoint accepts events at `POST /api/v1/events` and returns `202 Accepted` immediately. Processing is asynchronous.

```sh
curl -s -X POST http://localhost:8080/api/v1/events \
  -H "Content-Type: application/json" \
  -H "X-BugBarn-Api-Key: <your-api-key>" \
  -H "X-BugBarn-Project: default" \
  -d '{
    "severityText": "error",
    "body": "something went wrong",
    "exception": {
      "type": "RuntimeError",
      "message": "something went wrong",
      "stacktrace": [
        {"function": "main.run", "filename": "main.go", "lineno": 42}
      ]
    },
    "attributes": {
      "environment": "development"
    }
  }'
```

Refresh the issues list in the UI — the error will appear within seconds.

---

## 5. SDK quickstart

### Go

```sh
go get github.com/wiebe-xyz/bugbarn-go
```

```go
import bb "github.com/wiebe-xyz/bugbarn-go"

func main() {
    bb.Init(bb.Options{
        APIKey:      "your-api-key",
        Endpoint:    "http://localhost:8080",
        ProjectSlug: "default",
        Environment: "production",
    })
    defer bb.Shutdown(5 * time.Second)

    if err := doSomething(); err != nil {
        bb.CaptureError(err)
    }
}
```

For HTTP servers, wrap your handler to capture panics:

```go
http.ListenAndServe(":8080", bb.RecoverMiddleware(yourHandler))
```

### TypeScript / Node.js

```ts
import { init, captureException, shutdown } from "@bugbarn/typescript";

init({
  apiKey: "your-api-key",
  endpoint: "http://localhost:8080",
  project: "default",
});

try {
  doSomething();
} catch (err) {
  captureException(err);
}

await shutdown();
```

Each SDK exposes language-idiomatic wrappers around the same HTTP ingest endpoint.

For the full API and options, see [sdks/go.md](sdks/go.md) or [sdks/http.md](sdks/http.md) for direct HTTP integration.

---

## 6. Projects

Every event is scoped to a project. Pass the slug in the `X-BugBarn-Project` header or configure it in your SDK options. Unknown slugs are auto-created. The default project is named `default`.

To create a project explicitly:

```sh
bugbarn project create --slug my-service --name "My Service"
```

---

## 7. What you will see in the UI

Once events are flowing:

- **Issues list** — errors grouped by fingerprint, with event count, first/last seen, and status (unresolved / resolved / muted / regressed).
- **Event detail** — full payload: exception, stack trace, breadcrumbs, user context, attributes. Privacy scrubbing runs before storage.
- **All-projects view** — unified feed across every project on the instance. Each item carries a `project_slug` badge.
- **Log stream** — real-time structured logs over SSE, filterable by level and search string.

---

## 8. Install as an app (PWA)

BugBarn is a Progressive Web App. On Android, Chrome will offer an "Install BugBarn as an app" prompt in the bottom-right corner after you open the UI — tap **Install** to add it to your home screen and launch it in standalone mode. On desktop Chrome and Edge, the install icon appears in the address bar.

The prompt only appears once. If you dismissed it and want to install later, use the browser menu: **Chrome → Add to Home screen** (Android) or **Chrome → Install BugBarn** (desktop).

Service worker updates are versioned by a hash of the compiled assets. New deployments automatically invalidate the old cache — you will always get the latest version within one page load after a deploy.

---

## Next steps

- [Overview](overview.md) — capabilities, architecture, and what BugBarn is not
- [Deployment: configuration](deployment/configuration.md) — all environment variables
- [Deployment: Kubernetes](deployment/kubernetes.md) — production Kubernetes setup
- [API reference](api.md) — full REST API
