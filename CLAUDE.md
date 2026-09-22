# BugBarn

BugBarn is a self-hosted error tracking and analytics platform. It ingests errors from client SDKs, groups them by fingerprint, and provides a dashboard for triaging issues.

This file holds what is true across the repo. Package-specific notes live in a `CLAUDE.md`
next to the code (marked ★ below) and load when you work in that directory. Put a
package-specific trap in its shard, not here.

## Project Layout

```
cmd/bugbarn/          Server entrypoint; wires each role (main.go single/writer, reader.go reader, worker.go spool worker)
cmd/bb/               The bb CLI
internal/
  Write path ★ internal/ingest/CLAUDE.md
    ingest/           Ingest HTTP handler: auth, size limit, Validate, append to spool
    ingestproc/       Writer-side persistence pipeline for the Redis consumer, held-event replay
    ingestresp/       Ingest response contract (accepted / retryable / permanent drop)
    ingesthealth/     Ingest stall monitor, health endpoint status, out-of-band alerts
    queue/            Redis write queue between readers and the writer
    spool/            On-disk NDJSON event queue with cursor and dead letter
    worker/           ProcessRecord (decode, normalize, fingerprint), symbolication, worker status
    mutqueue/         Durable queue for admin issue mutations when the store is slow
    logparse/         Pino-style log body parsing, shared by the HTTP endpoint and the consumer
  Grouping ★ internal/fingerprint/CLAUDE.md
    fingerprint/      Fingerprint material and hash; changing it regroups production
    normalize/        SDK payload to event.Event mapping, Validate
    privacy/          Scrubbing of sensitive keys and values
    sourcemap/        Source map resolution for JS frames
    event/            Canonical ingest event type
    issues/           In-memory issue store used by the spool worker
  Storage ★ internal/storage/CLAUDE.md
    storage/          SQLite layer (single Store of domain stores), migrations, WAL checkpoint
    retention/        Writer-only sweep that expires events past their retention window
  Domain and services
    domain/           Domain types (Issue, Project, Event, Alert, ...)
    domainevents/     In-process bus: issue created / regressed / event recorded
    service/          Domain services (issues, projects, alerts, releases, logs, analytics)
    alert/            Alert evaluation and delivery (webhook, email)
    digest/           Periodic project summaries (mail, webhook)
    analytics/        Analytics types, queries and the rollup worker
    logstream/        SSE fan-out for live log tailing
  HTTP and auth
    api/              HTTP handlers and routing; reader-to-writer forwarders
    apperr/           Shared error types (NotFound, Conflict, InvalidInput, Internal)
    auth/             Local login and the opt-in OIDC adapter
    sessionstore/     Server-side web sessions; readers delegate writes to the writer
    oidctest/         In-process fake OIDC provider for tests
  Platform
    config/           Env-var configuration (BUGBARN_*)
    selflog/          ★ slog handler that reports errors to BugBarn itself
    tracing/          OpenTelemetry setup, HTTP middleware, tail sampler
    cli/              `bugbarn user|project` admin subcommands
web/                  SPA frontend (TypeScript, no framework)
sdks/                 Client SDKs (Go, TypeScript, Python, PHP); the server builds against sdks/go
deploy/               ★ Kustomize manifests per environment, Dockerfiles, packaging
specs/                Product specs and OpenAPI
docs/, site/          Documentation and the bugbarn.dev site (site mirrors docs)
```

## Runtime shape

Production and staging run one **writer** (`BUGBARN_MODE=writer`, the only process that
writes SQLite), several **readers** (`BUGBARN_MODE=reader`, read-only on the same PVC, HPA
scaled) and a **Redis** write queue between them. Testing runs a single-process monolith.
Readers forward every write to the writer. Details: `internal/ingest/CLAUDE.md` and
`deploy/CLAUDE.md`.

## Architecture Rules

**Error boundaries**: Each layer owns its error domain. Storage wraps DB errors into `apperr` types. Services log errors and pass through. API maps `apperr` codes to HTTP status codes. Never leak `sql.ErrNoRows` or raw DB errors past the storage layer.

**Logging**: Services are the log boundary. Storage doesn't log. API doesn't log errors. Services use `*slog.Logger` (constructor-injected). Structured JSON output.

**Testing**: Use real SQLite databases in tests (no mocks for storage). Service tests use fake repos. API tests use the full stack.

## Dogfooding

BugBarn reports its own errors to itself: `internal/selflog` captures every `slog` record
at ERROR and sends it to BugBarn's own ingest (enabled by `BUGBARN_SELF_ENDPOINT` and
`BUGBARN_SELF_API_KEY`), and `bb.RecoverMiddleware` catches handler panics.

- All error-level log paths MUST use `slog` (never stdlib `log.Printf`) so selflog captures them
- Log at ERROR only when something was lost; cancellations and shutdowns file bugs against ourselves
- After a deploy, check `bb issues --project bugbarn-service`

## Environments & Deployment

| Environment | Host | Namespace | Trigger |
|---|---|---|---|
| Testing | k3s1.nijmegen.wiebe.xyz | bugbarn-testing | Push to main (auto) |
| Staging | k3s1.nijmegen.wiebe.xyz | bugbarn-staging | Version tag `v*` (auto) |
| Production | layer7.wiebe.xyz | bugbarn-production | Version tag `v*`, after staging (auto) |

CI/CD runs on **Woodpecker** (`.woodpecker/`), triggered from Gitea. Push to `main` runs
tests, deploys testing and auto-tags a patch release; the tag deploys staging and then
production, with no manual step. Version tags MUST be lightweight. Secrets are
SOPS-encrypted under `deploy/`; never commit plaintext. The full chain, rollback and
secret files are in `deploy/CLAUDE.md`.

## bb CLI

The `bb` command-line tool is available for querying BugBarn. Use it to verify deployments, check issues, and monitor logs.

```bash
bb issues                              # list open issues (JSON)
bb issues --project bugbarn-service    # filter by project
bb issues --status all --query "panic" # search all issues
bb issue BW-3                          # get issue detail (Jira-style ID)
bb events BW-3                         # list events for an issue
bb resolve BW-3                        # resolve an issue
bb logs -f                             # live-tail structured logs
bb logs --project backend --level warn # filter by project and level
bb projects                            # list projects
bb projects --create "My App"          # create a project
```

Config lives at `~/.config/bugbarn/cli.json`. Authenticated against https://bugbarn.wiebe.xyz.

## Commands

- `go build ./...` — build
- `go test ./...` — run all tests
- `make spec-check` — validate OpenAPI specs
- `make quality` — the CI quality gates (file length, lint, soak, coverage, duplication)
- `bb issues` — check for open bugs (dogfood check)
