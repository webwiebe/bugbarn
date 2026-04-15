# Temu Sentry Constitution

## Core Principles

### I. Ingest First, Never Block on Storage

The ingest path MUST accept, authenticate, minimally validate, normalize enough to route, and durably enqueue events without performing transactional database inserts during the request. Any operation that can be moved to workers MUST be moved to workers. Backpressure MUST be explicit, observable, and bounded.

### II. Accept Real-World Payloads

The system MUST accept OpenTelemetry-shaped events as the canonical format and tolerate sender-specific error payloads through adapters. Unknown fields MUST be preserved after PII scrubbing. Invalid or partial payloads SHOULD produce best-effort events unless they cannot be authenticated or safely parsed.

### III. Privacy by Default

PII scrubbing MUST happen before events are persisted or exposed in the UI. Secrets, credentials, session tokens, obvious personal identifiers, raw IP addresses, and high-risk headers MUST be removed, hashed, truncated, or generalized by default. Privacy behavior MUST be testable and documented before release.

### IV. Low-Resource Operations

The project MUST run as Docker containers and SHOULD also run as standalone binaries. The default deployment target is low-powered hardware such as a Raspberry Pi, small VM, or colocated sidecar. Dependencies MUST justify their CPU, memory, disk, and operational cost.

### V. Spec-Driven Agent Work

New work MUST start from a Spec Kit artifact or update an existing artifact before implementation. Specs define behavior, plans define architecture, tasks define execution. CI MUST eventually verify that implementation, tests, and documentation remain aligned with accepted specs.

## Architecture Constraints

- Ingest service: Go by default.
- Durable ingest buffer: append-only local disk spool or embedded queue before database persistence.
- Persistence: embedded-first or low-ops database choices are preferred until scale proves otherwise.
- Frontend: lightweight, API-driven, and deployable separately from ingest.
- SDKs: TypeScript and Python are first-class release targets.
- Auth: API keys for applications; username/password for human users; configuration via CLI and/or environment variables.

## Quality Gates

- Every ingest change MUST include load-oriented tests or benchmarks that exercise the non-blocking path.
- Every normalization or scrubbing change MUST include fixture-based tests.
- Every SDK release MUST include uncaught exception/default handler coverage.
- Every UI workflow MUST be backed by API contract tests and at least one browser-level smoke test once the UI exists.
- CI/CD changes MUST be reproducible locally through documented `make` targets.

## Governance

This constitution overrides ad hoc implementation choices. Amendments require updating this file and any affected specs before code changes land. Feature specs may tighten constraints but may not weaken these principles without a constitution amendment.

**Version**: 0.1.0 | **Ratified**: 2026-04-15 | **Last Amended**: 2026-04-15

