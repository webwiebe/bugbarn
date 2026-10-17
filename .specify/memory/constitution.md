# BugBarn Constitution

These are the rules the codebase already enforces. Each principle names the
test, gate or issue that backs it. A principle with no source in the tree is
listed under "Not ratified".

## Core Principles

### I. Ingest first, never block on storage

The ingest path MUST accept, authenticate, minimally validate and durably enqueue
events without performing transactional database inserts during the request. Work
that can move to workers MUST move to workers. Backpressure MUST be explicit and
bounded: oversize bodies are refused, and a stalled ingest is reported.

Sources:
- `internal/ingest/handler_test.go::TestServeHTTPAcceptedAndSpoolsBody`, `::TestServeHTTPRejectsOversizeBody`, `::TestServeHTTPRejectsMissingAPIKeyWhenEnabled`
- `internal/spool/spool_test.go::TestDeadLetterAppend`
- `internal/ingesthealth/monitor_test.go::TestSampleStalledIngest`
- `internal/ingest/CLAUDE.md` (the write path), [specs/007-redis-write-queue](../../specs/007-redis-write-queue/spec.md)

### II. Accept real-world payloads

The canonical event format is OpenTelemetry-shaped, and sender-specific payloads
MUST be accepted through adapters. Invalid or partial payloads SHOULD produce
best-effort events unless they cannot be authenticated or safely parsed.

Sources:
- `internal/normalize/normalize_test.go::TestNormalizeOpenTelemetryShapedEvent`, `::TestNormalizeSDKStyleEvent`
- `internal/ingest/alertmanager_test.go::TestServeAlertmanagerHTTPSpoolsOneRecordPerAlert`

### III. Privacy by default

Secrets, credentials, session tokens and obvious personal identifiers MUST be
removed before events are persisted or shown. Raw IP addresses MUST be dropped
unless an operator opts in to keeping them.

Sources:
- `internal/privacy/scrub_test.go::TestScrubRedactsSensitiveKeysAndStringPatterns`
- `internal/privacy/options_test.go::TestScrubWithKeepIPs`

### IV. Grouping is stable

The fingerprint hash decides which events share an issue. Volatile identifiers
(UUIDs, timestamps, addresses, source locations) MUST NOT change it, and a change
to its material regroups production, so it MUST come with tests.

Sources:
- `internal/fingerprint/fingerprint_test.go::TestFingerprintIgnoresVolatileIdentifiers`, `::TestFingerprintIgnoresSourceLocations`, `::TestFingerprintChangesForDifferentExceptionType`
- `internal/fingerprint/timestamp_test.go::TestFingerprintGroupsMessagesDifferingOnlyByTimestamp`
- `internal/fingerprint/CLAUDE.md`

### V. One writer, many readers

Only the writer process writes SQLite. Readers open the store read-only and
forward every write to the writer. The error boundary is fixed: storage wraps
database errors into `apperr` types, services log, and the API maps codes to HTTP
statuses.

Sources:
- `internal/storage/readonly_test.go::TestOpenReadOnly_PersistProcessedEventFails`
- `internal/api/server_forwarding_test.go::TestWriteForwarder_AuthenticatedPOSTForwarded`, `::TestWriteForwarder_LoginForwarded`
- [specs/006-cqrs-read-write-split](../../specs/006-cqrs-read-write-split/spec.md), `deploy/CLAUDE.md`

### VI. Quality metrics only move toward better

File length, complexity, duplication and coverage are gated in CI against
committed baselines. A new violation MUST be fixed, not baselined.

Sources:
- `make quality` (file length, lint, soak, coverage, duplication), run by the `quality` step in `.woodpecker/ci.yaml`
- `scripts/` gate tests run by `make test-gates`

### VII. BugBarn reports its own failures

Error-level log paths MUST use `slog` so the self-reporting handler captures them,
and a log at ERROR means something was lost.

Sources:
- `internal/selflog/handler_test.go::TestErrorReportCarriesAttributes`, `::TestBelowErrorIsNotReported`
- `internal/selflog/CLAUDE.md`

### VIII. Production runs what staging validated

Merges to `main` deploy testing, a version tag deploys staging, and production
follows once staging is green, with a preflight and an automatic rollback.

Sources:
- `.woodpecker/deploy-production.yaml` (`depends_on` the release pipeline, preflight, rollback), `.woodpecker/tag-check.yaml`
- `deploy/CLAUDE.md`

### IX. Specs describe the tree

New work starts as a spec or a spec change, and a behaviour change updates its
spec in the same PR. Where documents disagree with the code, the code is the fact.

Sources:
- `specs/README.md` (workflow), `AGENTS.md` and `CLAUDE.md` (Specs And Constitution)
- `make spec-check` (the spec files exist)

## Architecture Constraints

- Ingest service: Go. Durable ingest buffer: local disk spool, or the Redis write queue between readers and the writer.
- Persistence: embedded SQLite until scale proves otherwise.
- Frontend: lightweight, API-driven, deployable separately from ingest.
- SDKs: TypeScript and Python are first-class; PHP and Go ship as well.
- Auth: API keys for applications; username and password, or OIDC, for people.

## Not ratified

Candidates checked against the tree and left out because nothing enforces them:

- Low-resource operation on Raspberry Pi-class hardware (original principle IV). No test, gate or budget measures it, and production runs on a cluster.
- "CI MUST eventually verify that implementation, tests and documentation stay aligned with specs" (original principle V). `make spec-check` tests that files exist and nothing else; a gate that ties code changes to spec changes does not exist.
- Every ingest change includes a benchmark of the non-blocking path (original quality gate). No gate requires it.
- Every SDK release covers uncaught-exception handlers (original quality gate). Not checked in CI.

## Governance

- This file changes through a pull request that edits the principle and names
  the gate, test, ADR or issue that now backs the change. A principle whose
  source is removed from the tree is removed or amended in the same PR.
- A principle is tightened by adding a gate or test. It is weakened only by an
  ADR that says why.
- Every spec lists the principles it touches in its "Principles touched" line,
  and every plan answers the Constitution Check in the plan template.
- Where this file and the agent rule (`AGENTS.md`, `CLAUDE.md`) disagree, fix
  whichever one is wrong against the tree in the same PR.

**Version**: 0.2.0 | **Ratified**: 2026-04-15 | **Last Amended**: 2026-10-17
