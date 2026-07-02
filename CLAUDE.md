# BugBarn

BugBarn is a self-hosted error tracking and analytics platform. It ingests errors from client SDKs, groups them by fingerprint, and provides a dashboard for triaging issues.

## Project Layout

```
cmd/bugbarn/         — Main binary entrypoint
internal/
  api/              — HTTP handlers (maps service errors → HTTP status codes)
  apperr/           — Shared error types (NotFound, Conflict, InvalidInput, Internal)
  analytics/        — Analytics types and query definitions
  auth/             — Session/cookie auth, API key validation
  domain/           — Domain types (Issue, Project, Event, Alert, etc.)
  event/            — Ingest event parsing
  fingerprint/      — Error grouping/deduplication logic
  ingest/           — Ingest pipeline (receives events from SDKs)
  service/          — Domain services (issues, projects, alerts, releases, logs, analytics)
  selflog/          — slog handler that reports errors to BugBarn itself
  storage/          — SQLite storage layer (single Store struct)
  worker/           — Background event processor
  spool/            — On-disk event queue
web/                — SPA frontend (TypeScript, no framework)
sdks/               — Client SDKs (Go, TypeScript, Python, PHP)
deploy/k8s/         — Kubernetes manifests (testing, staging, production)
specs/              — OpenAPI specs
site/               — Marketing site (bugbarn.dev)
```

## Architecture Rules

**Error boundaries**: Each layer owns its error domain. Storage wraps DB errors into `apperr` types. Services log errors and pass through. API maps `apperr` codes to HTTP status codes. Never leak `sql.ErrNoRows` or raw DB errors past the storage layer.

**Logging**: Services are the log boundary. Storage doesn't log. API doesn't log errors. Services use `*slog.Logger` (constructor-injected). Structured JSON output.

**Testing**: Use real SQLite databases in tests (no mocks for storage). Service tests use fake repos. API tests use the full stack.

## Dogfooding

BugBarn reports its own errors to itself. This is critical — if something breaks, it must show up in our own dashboard.

How it works:
- `internal/selflog/handler.go` wraps `slog.Handler` — on `Level >= Error`, it calls `bb.CaptureMessage()` to report to BugBarn's own ingest endpoint
- The Go SDK (`github.com/wiebe-xyz/bugbarn-go`) is imported as `bb` in `cmd/bugbarn/main.go`
- `bb.RecoverMiddleware` catches panics in HTTP handlers
- Self-reporting is configured via `BUGBARN_SELF_DSN` env var

Rules:
- All error-level log paths MUST use `slog` (never stdlib `log.Printf`) so selflog captures them
- When adding error handling, ensure the error propagates through a service that logs at error level
- Test that errors actually appear in BugBarn by checking the dashboard after deploys
- The `bb` CLI can be used to verify: `bb issues --project bugbarn-service`

## Environments & Deployment

| Environment | Host | Namespace | Trigger |
|---|---|---|---|
| Testing | k3s1.nijmegen.wiebe.xyz | bugbarn-testing | Push to main (auto) |
| Staging | k3s1.nijmegen.wiebe.xyz | bugbarn-staging | Version tag `v*` (auto) |
| Production | layer7.wiebe.xyz | bugbarn-production | Version tag `v*`, after staging (auto) |

CI/CD runs on **Woodpecker** (`.woodpecker/`), triggered from Gitea. The whole
chain is automatic — no manual steps.

### Deployment Steps (fully automated)

1. Push to `main` → `ci` (tests) + `build-and-test` (builds SHA-tagged images, deploys **testing**) + `binary-release` (auto-bumps a patch `vX.Y.Z` tag)
2. The tag fires the tag pipeline: `release` retags the images to semver and deploys **staging** by digest
3. `deploy-production` (`depends_on: release`) runs in that same tag pipeline: it deploys **production** only after staging rolls out successfully — preflight verifies the semver images are in GHCR, deploys by immutable digest, and rolls back automatically on failure

### Important

- Images are tagged by commit SHA during CI, then retagged to semver on release
- Production auto-deploys on the version tag; the safety gate is `depends_on: release` (staging must succeed first) plus preflight + automatic rollback — no manual confirmation
- Version tags MUST be lightweight (`binary-release` creates them that way); an annotated tag makes Woodpecker report the tag-object sha as `CI_COMMIT_SHA` and breaks the retag
- Secrets are SOPS-encrypted in `deploy/k8s/*/secret.yaml` — never commit plaintext secrets
- After updating any K8s secret, always `kubectl rollout restart` the affected deployment
- Production posts a release marker to BugBarn's own API after deploy

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
- `bb issues` — check for open bugs (dogfood check)
