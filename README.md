# BugBarn

Barebones self-hosted error tracking for personal infrastructure and small projects.

This repository is being developed spec-first with GitHub Spec Kit. The root product ask is captured in `specs/001-personal-error-tracker/` and governed by `.specify/memory/constitution.md`.

## Docs

- [`docs/architecture.md`](docs/architecture.md): repository layout, runtime flow, and ownership boundaries
- [`specs/001-personal-error-tracker/spec.md`](specs/001-personal-error-tracker/spec.md): product requirements and user stories
- [`specs/001-personal-error-tracker/plan.md`](specs/001-personal-error-tracker/plan.md): implementation plan and architecture
- [`specs/001-personal-error-tracker/tasks.md`](specs/001-personal-error-tracker/tasks.md): implementation backlog and status
- [`docs/mvp-acceptance.md`](docs/mvp-acceptance.md): short validation checklist for the current MVP surface

## Initial Direction

- Fast Go ingest service that accepts high-volume error/event payloads without doing transactional database writes in the request path.
- Durable local spool plus background workers for normalization, PII scrubbing, fingerprinting, facet extraction, and persistence.
- OpenTelemetry-shaped event model with permissive adapters for sender-specific formats.
- TypeScript SDK support for release/dist metadata on captured events and source map uploads to the same backend.
- Web interface for issue lists, event drill-down, and live event flow.
- First SDKs for TypeScript and Python with default error-handler integration, with a future PHP SDK tracked separately.
- Docker-first deployment with standalone binary support for low-powered hardware.
- Homelab staging/testing deployment through GitHub Actions and K3S.

## Spec Kit Workflow

Use the documents in `specs/001-personal-error-tracker/` as the source of truth:

- `spec.md`: user scenarios and functional requirements
- `plan.md`: technical approach and architecture
- `tasks.md`: implementation backlog
- `contracts/ingest-api.yaml`: initial API contract
- `contracts/source-maps-api.yaml`: source map upload contract for release/dist-linked artifacts

## Local Development

```bash
make test
make build
make dev
```

The initial Go service reads these development defaults:

- `BUGBARN_ADDR`: listen address, default `:8080`
- `BUGBARN_API_KEY`: optional ingest key; Docker Compose uses `local-dev-key`
- `BUGBARN_SPOOL_DIR`: durable local spool directory, default `.data/spool`
- `BUGBARN_MAX_BODY_BYTES`: request body limit, default `1048576`
- `BUGBARN_MAX_SPOOL_BYTES`: optional durable spool size limit; returns retryable backpressure when full

Send a local event:

```bash
curl -X POST http://localhost:8080/api/v1/events \
  -H 'content-type: application/json' \
  -H 'x-bugbarn-api-key: local-dev-key' \
  --data @specs/001-personal-error-tracker/fixtures/example-event.json
```

Process the local spool once and print a summary:

```bash
BUGBARN_SPOOL_DIR=.data/spool go run ./cmd/bugbarn worker-once
```

## Homelab Runner Scaffold

The `infra/` directory contains a minimal Ansible scaffold for provisioning project-specific GitHub Actions runners. It follows the shape of the adjacent `../rapid-root/infra` setup and expects those shared runner roles to be available locally unless they are vendored later.
