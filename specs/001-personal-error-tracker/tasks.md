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

- [ ] T011 Implement API key authentication middleware with hashed key storage.
- [x] T012 Implement request size limits and content-type handling.
- [ ] T013 Implement append-only local disk spool with segment files and generated ingest IDs.
- [ ] T014 Implement spool recovery on process start.
- [x] T015 Implement explicit backpressure when spool size or disk limits are reached.
- [x] T016 Add ingest endpoint returning `202`, `401`, `413`, `429`, and `503` according to contract.
- [x] T017 Add ingest benchmarks proving no issue/event database insert occurs in the request path.

## Phase 4: Normalization, Privacy, and Grouping

- [ ] T018 Implement worker loop that reads, leases, retries, and dead-letters spool records.
- [x] T019 Implement canonical OpenTelemetry-shaped event normalization.
- [ ] T020 Implement best-effort handling for unknown and partial sender payloads.
- [x] T021 Implement privacy scrubber by sensitive key patterns.
- [x] T022 Implement privacy scrubber by sensitive value patterns.
- [x] T023 Implement fingerprint normalization for exception type, message, stack frames, and stable context.
- [x] T024 Implement issue create/update logic from fingerprints.
- [ ] T025 Implement event persistence linked to issues.

## Phase 5: Storage and Facets

- [ ] T026 Implement SQLite schema for users, projects, API keys, raw ingest metadata, issues, events, facet keys, and event facets.
- [ ] T027 Add migrations and local database initialization.
- [ ] T028 Implement facet discovery from scrubbed resource and attributes JSON.
- [ ] T029 Implement typed facet value persistence.
- [ ] T030 Implement facet cardinality safeguards and indexing controls.
- [ ] T031 Add query APIs for issues, issue detail, events, event detail, and facets.

## Phase 6: Auth and Administration

- [ ] T032 Implement admin user bootstrap from environment variables.
- [ ] T033 Implement CLI command to create/update admin users.
- [ ] T034 Implement username/password login with secure password hashing.
- [ ] T035 Implement project creation CLI/API.
- [ ] T036 Implement API key creation, display-once secret generation, revocation, and last-used tracking.

## Phase 7: Web UI

- [ ] T037 Choose lightweight frontend stack and document the decision in `research.md`.
- [ ] T038 Implement login flow.
- [ ] T039 Implement issue list with sort, count, severity, first seen, last seen, project, and selected facets.
- [ ] T040 Implement issue detail with normalized exception data and scrubbed context.
- [ ] T041 Implement previous/next event navigation for an issue.
- [ ] T042 Implement live events view using SSE or websocket with reconnect.
- [ ] T043 Add browser smoke tests for login, issue list, issue detail, and live events.

## Phase 8: SDKs

- [x] T044 Create TypeScript SDK package with initialization, manual capture, async transport, and uncaught handler support.
- [x] T045 Create TypeScript sample app for uncaught exceptions and unhandled promise rejections.
- [x] T046 Create Python SDK package with initialization, manual capture, async transport, and `sys.excepthook` support.
- [x] T047 Create Python sample app for uncaught exceptions.
- [ ] T048 Add SDK shutdown/flush with bounded timeout.

## Phase 9: Deployment and Homelab CI/CD

- [x] T049 Add Dockerfiles for service/worker and web.
- [x] T050 Add K3S manifests or Helm/Kustomize overlays for testing and staging.
- [x] T051 Add namespaces `bugbarn-testing` and `bugbarn-staging`.
- [ ] T052 Add GitHub Actions deploy workflow for testing on main branch or selected branches.
- [ ] T053 Add GitHub Actions deploy workflow for staging on tagged or manually dispatched builds.
- [x] T054 Add project-specific self-hosted runner definitions using the `infra/` Ansible scaffold.

## Phase 10: Release Readiness

- [ ] T055 Document local deployment, Docker deployment, binary deployment, and homelab deployment.
- [ ] T056 Add security notes covering auth model, API key storage, PII scrubbing, and personal-use assumptions.
- [ ] T057 Add operational docs for spool sizing, backpressure, retention, backup, and recovery.
- [ ] T058 Run sustained ingest benchmark and record baseline hardware/resource usage.
- [ ] T059 Verify all success criteria in `spec.md`.
- [ ] T060 Prepare first public release checklist.
- [x] T062 Add a compact MVP acceptance checklist for fixtures, sample apps, and load validation.

## Phase 11: Future SDKs

- [ ] T061 Add a PHP SDK package with initialization, manual capture, async transport, and uncaught handler support. This is intentionally left for a later iteration after the foundation SDKs.
