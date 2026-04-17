# Tasks: Personal Error Tracker Foundation

**Input**: Design documents from `/specs/001-personal-error-tracker/`

## Phase 1: Repository and Tooling Setup

- [x] T001 Create monorepo skeleton: `cmd/`, `internal/`, `web/`, `sdks/`, `deploy/`, and `infra/`.
- [x] T002 Initialize Go module and baseline CLI entrypoint for `bugbarn`.
- [x] T003 Add Makefile targets for `setup`, `test`, `lint`, `build`, `dev`, and `docker-build`.
- [x] T004 Add GitHub Actions CI for Go tests, SDK tests, frontend checks, and container builds.
- [x] T005 Add Docker Compose for local single-node development.

## Phase 2: Contracts and Test Fixtures

- [x] T006 Add OpenAPI contract validation for `contracts/ingest-api.yaml`.
- [x] T007 Create canonical event fixtures covering OpenTelemetry-shaped JSON, minimal JSON, malformed best-effort JSON, and sender-specific variants.
- [x] T008 Create PII scrubbing fixtures for emails, raw IPs, authorization headers, cookies, tokens, session IDs, UUIDs, and high-cardinality values.
- [x] T009 Create fingerprinting fixtures that prove volatile values collapse to stable fingerprints.
- [x] T010 Add load-test fixture generator for high-volume small events.

## Phase 3: Ingest and Durable Spool

- [x] T011 Implement API key authentication middleware with hashed key storage.
- [x] T012 Implement request size limits and content-type handling.
- [x] T013 Implement append-only local disk spool with segment files and generated ingest IDs. (async in-memory channel, background drain with batched fsync, project slug stored in spool records)
- [x] T014 Implement spool recovery on process start. (cursor persisted via spool.ReadCursor/WriteCursor; runBackgroundWorker reads cursor offset at startup and resumes from last committed position)
- [x] T015 Implement explicit backpressure when spool size or disk limits are reached.
- [x] T016 Add ingest endpoint returning `202`, `401`, `413`, `429`, and `503` according to contract.
- [x] T017 Add ingest benchmarks proving no issue/event database insert occurs in the request path.

## Phase 4: Normalization, Privacy, and Grouping

- [x] T018 Implement worker loop that reads, leases, retries, and dead-letters spool records. (32k-slot async queue, background goroutine with batched writes, 429 backpressure when full; ~400 req/s throughput)
- [x] T019 Implement canonical OpenTelemetry-shaped event normalization.
- [x] T020 Implement best-effort handling for unknown and partial sender payloads.
- [x] T021 Implement privacy scrubber by sensitive key patterns.
- [x] T022 Implement privacy scrubber by sensitive value patterns.
- [x] T023 Implement fingerprint normalization for exception type, message, stack frames, and stable context.
- [x] T024 Implement issue create/update logic from fingerprints.
- [x] T025 Implement event persistence linked to issues.
- [x] T077 Introduce a service layer and repository boundary so HTTP handlers do not own business logic or SQL details.

## Phase 5: Storage and Facets

- [x] T026 Implement SQLite schema for users, projects, API keys, raw ingest metadata, issues, events, facet keys, and event facets. (internal/storage/schema.go; raw ingest lives in durable spool on disk, not DB)
- [x] T027 Add migrations and local database initialization. (idempotent CREATE TABLE IF NOT EXISTS applied at Open; schema_version tracking via simple sequential ALTER guards)
- [x] T028 Implement facet discovery from scrubbed resource and attributes JSON. (internal/storage/facets.go extractFacets; pulls resource + attributes fields plus environment/release/severity)
- [x] T029 Implement typed facet value persistence. (PersistFacets in internal/storage/facets.go; stores project_id, event_id, issue_id, section, facet_key, facet_value)
- [x] T030 Implement facet cardinality safeguards and indexing controls. (maxFacetKeysPerProject=50, maxFacetValuesPerKey=10000; idx_event_facets_lookup index)
- [x] T031 Add query APIs for issues, issue detail, events, event detail, and facets. (internal/api/issues.go, events.go, facets.go; GET /api/v1/issues, /issues/:id, /events/:id, /facets/:key)

## Phase 6: Auth and Administration

- [x] T032 Implement admin user bootstrap from environment variables.
- [x] T033 Implement CLI command to create/update admin users. (`bugbarn user create --username=X --password=Y` in cmd/bugbarn/main.go runUserCmd; upserts via storage.UpsertUser)
- [x] T034 Implement username/password login with secure password hashing.
- [x] T035 Implement project creation CLI/API. (auto-create on `apikey create`, explicit `project create` subcommand, `/api/v1/projects` endpoint, web UI project switcher, X-BugBarn-Project header routing)
- [x] T036 Implement API key creation, display-once secret generation, revocation, and last-used tracking.

## Phase 7: Web UI

- [x] T037 Choose lightweight frontend stack, keep browser-side source TypeScript-first, and document the decision in `research.md`.
- [x] T038 Implement login flow.
- [x] T039 Implement issue list with sort, count, severity, first seen, last seen, project, and selected facets. (renderIssueListMarkup in web/src/components.ts; sort by last_seen/first_seen/event_count; severity/count/first/last seen columns; facet filter via ?attributes.environment=X)
- [x] T040 Implement issue detail with normalized exception data and scrubbed context.
- [x] T041 Implement previous/next event navigation for an issue.
- [x] T042 Implement live events view using SSE or websocket with reconnect. (startLiveStream/SSE in web/src/app.ts; exponential backoff reconnect; GET /api/v1/events/stream backend in internal/api/events.go)
- [x] T043 Add browser smoke tests for login, issue list, issue detail, and live events. (web/e2e/smoke.spec.ts with Playwright; covers login, issue list, issue detail navigation, live events panel; run with npm run test:e2e against a live server)
- [x] T065 Add dark-mode web theme and richer event detail sections for exception, context, stacktrace, spans, and scrubbed payload data.
- [x] T070 Add a Sentry-inspired issue overview/detail layout pass with dense issue columns and issue/event summary headers.
- [x] T071 Remove nonfunctional mirrored UI chrome, fake graphs, disabled actions, and placeholder rails until backing data exists.
- [x] T072 Split the browser UI into typed data, domain, formatting, and component modules.
- [x] T073 Add Releases, Alerts, and Settings views backed by same-origin routes with real forms and empty states.
- [x] T074 Add issue resolve/reopen controls plus source map upload and settings forms.
- [x] T075 Improve traceback rendering with fingerprint material and concise source snippets when provided by the backend.

## Phase 8: SDKs

- [x] T044 Create TypeScript SDK package with initialization, manual capture, async transport, and uncaught handler support.
- [x] T045 Create TypeScript sample app for uncaught exceptions and unhandled promise rejections.
- [x] T046 Create Python SDK package with initialization, manual capture, async transport, and `sys.excepthook` support.
- [x] T047 Create Python sample app for uncaught exceptions.
- [x] T048 Add SDK shutdown/flush with bounded timeout.
- [x] T064 Make the TypeScript SDK build into an installable package tarball served by the BugBarn web container for Rapid Root integration.
- [x] T076 Add TypeScript release/dist event metadata capture and source map upload helper with a documented multipart backend contract.

## Phase 9: Deployment and Homelab CI/CD

- [x] T049 Add Dockerfiles for service/worker and web.
- [x] T050 Add K3S manifests or Helm/Kustomize overlays for testing and staging.
- [x] T051 Add namespaces `bugbarn-testing` and `bugbarn-staging`.
- [x] T052 Add GitHub Actions deploy workflow for testing on main branch or selected branches.
- [x] T053 Add GitHub Actions deploy workflow for staging on tagged or manually dispatched builds.
- [x] T054 Add project-specific self-hosted runner definitions using the `infra/` Ansible scaffold.
- [x] T063 Expose testing and staging overlays through K3S ingress hostnames for web/API access.

## Phase 10: Release Readiness

- [x] T055 Document local deployment, Docker deployment, binary deployment, and homelab deployment.
- [x] T056 Add security notes covering auth model, API key storage, PII scrubbing, and personal-use assumptions.
- [x] T057 Add operational docs for spool sizing, backpressure, retention, backup, and recovery. (docs/operations.md covers spool sizing, backpressure, retention policy, backup/restore, spool-only and DB-only recovery, admin bootstrap, env vars)
- [x] T058 Run sustained ingest benchmark and record baseline hardware/resource usage. (scripts/loadtest/main.go; baseline ~400 req/s at c=64 on Raspberry Pi-class k3s node)
- [x] T059 Verify all success criteria in `spec.md`. (docs/release-checklist.md; all 7 SC verified passing as of 2026-04-18)
- [x] T060 Prepare first public release checklist. (docs/release-checklist.md; covers pre-release, security review, deployment verification, version tagging, and post-release steps)
- [x] T062 Add a compact MVP acceptance checklist for fixtures, sample apps, and load validation.

## Phase 11: Future SDKs

- [x] T061 Add a PHP SDK package with initialization, manual capture, async transport, and uncaught handler support. (sdks/php/; Client::init/captureException/captureMessage/flush/shutdown; in-process queue flushed via register_shutdown_function; set_exception_handler + fatal error shutdown hook; curl transport with 2s timeout; PHPUnit tests; CI gated on php+composer availability)

## Phase 12: Release Markers

- [x] T066 Add release/notable-event marker persistence with project, environment, observed time, version/commit, URL, notes, and creator fields.
- [x] T067 Add release marker API endpoints for creating, listing, and querying nearby markers for an issue/event.
- [X] T068 Add web UI timeline markers so regressions can be visually linked to recent deploys.
- [x] T069 Add CI/GitHub Actions example for posting a BugBarn release marker after testing and staging deploys. (docs/ci-integration.md updated; monorepo matrix pattern and per-component API key guidance included)
