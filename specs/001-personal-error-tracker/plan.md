# Implementation Plan: Personal Error Tracker Foundation

**Branch**: `001-personal-error-tracker` | **Date**: 2026-04-15 | **Spec**: `specs/001-personal-error-tracker/spec.md`

## Summary

Build a small self-hosted error tracker with a Go ingest/API/worker service, local durable request-path spool, OpenTelemetry-shaped canonical events, privacy-first normalization, issue grouping, flexible facets, TypeScript/Python SDKs, and a lightweight web UI. The first release optimizes for personal single-node operation while leaving room for Postgres/K3S staging and future production hardening.

## Technical Context

**Language/Version**: Go 1.24+ for ingest/API/worker; TypeScript for web and TS SDK; Python 3.11+ for Python SDK. Browser-side scripting source should be TypeScript, not hand-written JavaScript.
**Primary Dependencies**: Go standard HTTP stack, OpenTelemetry semantic conventions where useful, SQLite driver, frontend framework selected during implementation, OpenAPI tooling
**Storage**: Local append-only disk spool in request path; SQLite default for processed issues/events/facets behind repository interfaces; consider `sqlc` for typed SQL generation once queries stabilize
**Testing**: Go unit/integration/benchmark tests, SDK tests, frontend component/browser smoke tests, OpenAPI contract checks, load fixtures
**Target Platform**: Linux containers, standalone Go binary, Raspberry Pi-class hardware, K3S testing/staging
**Project Type**: Monorepo with service, web app, SDK packages, and infra manifests
**Performance Goals**: p95 ingest below 10 ms for durable enqueue under normal load; explicit backpressure instead of unbounded memory; initial benchmark target of 1,000 small events/sec on dev hardware
**Constraints**: No request-path issue/event DB inserts; scrub before persistence/UI; no external SaaS dependencies required; simple personal-use auth

## Constitution Check

- [x] Ingest does not block on transactional storage: request path writes only to durable local spool.
- [x] Unknown payload fields are preserved after scrubbing: canonical envelope allows additional properties and scrubbed raw JSON.
- [x] PII is scrubbed before persistence/UI exposure: scrubbing worker is required before event storage.
- [x] Low-resource operation is considered: Go binary, SQLite default, optional separate web container.
- [x] Tests are specified for critical behavior: load, grouping, scrubbing, SDK handlers, UI smoke tests.

## Project Structure

```text
cmd/
  bugbarn/
    main.go
internal/
  ingest/
  spool/
  worker/
  normalize/
  privacy/
  fingerprint/
  storage/
  facets/
  auth/
  api/
web/
  app/
sdks/
  typescript/
  python/
deploy/
  docker/
  k8s/
infra/
  Makefile
  *.yml
specs/
  001-personal-error-tracker/
```

## Architecture

### Runtime Components

- **`cmd/bugbarn`**: Process entrypoint. It wires config, storage, the spool, the HTTP server, and the background worker together. `worker-once` is the maintenance path for draining the current spool once.
- **HTTP/API layer**: `internal/api` owns route dispatch, request parsing, authentication/session handling, and response formatting.
- **Service layer**: `internal/service` owns business use cases and calls repository interfaces.
- **Worker**: `internal/worker` reads spool records, parses payloads, normalizes to canonical events, scrubs PII, computes fingerprints, and hands processed events to storage.
- **Repositories**: `internal/storage` owns SQLite schema creation, migrations, issue/event/facet persistence, and read/query operations behind repository interfaces.
- **Web UI**: Separate deployable browser client that stays TypeScript-first and calls read/live APIs.
- **SDKs**: Lightweight clients that capture errors, build canonical envelopes, and send asynchronously.

### Ingest Flow

1. Authenticate `x-bugbarn-api-key`.
2. Enforce request size and content type limits.
3. Append raw request bytes plus request metadata to the local durable spool.
4. Return `202 Accepted` with an ingest ID.
5. Worker reads records, normalizes, scrubs, fingerprints, persists, and acknowledges spool offsets.

### Route Ownership

- Ingest endpoint contract: `specs/001-personal-error-tracker/contracts/ingest-api.yaml`
- HTTP route wiring: `internal/api/server.go`
- Browser UI expectations: `web/README.md`

### Grouping Flow

1. Extract exception type, message, stack frames, severity, and stable context.
2. Normalize volatile tokens from messages, paths, and frame data.
3. Hash the normalized fingerprint material.
4. Create or update the matching issue.
5. Persist the event linked to the issue.

### Facet Flow

1. Traverse scrubbed resource and attributes JSON.
2. Register discovered stable keys as facet keys.
3. Store typed facet values for query/filter operations.
4. Maintain issue-level summaries asynchronously where needed for speed.

### Privacy Flow

1. Scrub raw request metadata before persistence.
2. Scrub payload fields by key patterns, value patterns, and known sensitive paths.
3. Hash or generalize IP-like values and user identifiers.
4. Persist only scrubbed payload/context.
5. Expose only scrubbed values in APIs and UI.

## API Surface

- `POST /api/v1/events`: application ingest
- `GET /api/v1/issues`: issue list with filters/sort
- `GET /api/v1/issues/{id}`: issue detail
- `GET /api/v1/issues/{id}/events`: linked events
- `GET /api/v1/events/{id}`: event detail
- `GET /api/v1/facets`: discovered facets
- `GET /api/v1/live/events`: server-sent events or websocket live stream
- `GET /api/v1/releases`: release marker list
- `POST /api/v1/releases`: create release marker
- `GET /api/v1/alerts`: alert rule list
- `POST /api/v1/alerts`: create alert rule
- `GET /api/v1/settings`: session/workspace settings
- `POST /api/v1/settings`: update settings
- `POST /api/v1/source-maps`: upload source maps or artifact metadata
- `POST /api/v1/issues/{id}/resolve`: mark issue resolved
- `POST /api/v1/issues/{id}/reopen`: reopen resolved issue
- `POST /api/v1/login`: user login
- `POST /api/v1/projects`: project management
- `POST /api/v1/projects/{id}/keys`: API key management

## CI/CD Plan

- GitHub Actions for lint, test, build, SDK checks, image build, and OpenAPI validation.
- Self-hosted runner labels:
  - `build` for regular CI jobs.
  - `deploy` for K3S namespace deploy jobs.
  - `macos,arm64` for native macOS/ARM checks if needed.
- K3S namespaces:
  - `bugbarn-testing`
  - `bugbarn-staging`
- Production is deferred.

## Risk Log

| Risk | Impact | Mitigation |
|------|--------|------------|
| Local spool corruption | Accepted events may be lost or block workers | Segment files, checksums, atomic append discipline, recovery tests |
| SQLite write contention | Worker throughput may lag during bursts | Batch writes, WAL mode, bounded worker concurrency, optional Postgres adapter later |
| Over-aggressive PII scrubbing | Useful debugging context may be lost | Document defaults, allow safe configurable rules, test representative fixtures |
| Under-aggressive PII scrubbing | Sensitive data may be stored | Denylist sensitive keys, pattern scrubbing, fixture tests, UI/API only reads scrubbed payloads |
| Facet explosion | High-cardinality data may bloat storage | Key registry, cardinality limits, hashing/generalization, opt-in indexing |
| SDK shutdown behavior | Errors may be lost or apps may hang | Bounded async queues, flush with timeout, documented tradeoff |
| K3S homelab coupling | Open-source users may not share infra | Keep homelab deployment under `infra/`/`deploy/` and Docker path generic |
