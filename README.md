# BugBarn

Barebones self-hosted error tracking for personal infrastructure and small projects.

This repository is being developed spec-first with GitHub Spec Kit. The root product ask is captured in `specs/001-personal-error-tracker/` and governed by `.specify/memory/constitution.md`.

## Initial Direction

- Fast Go ingest service that accepts high-volume error/event payloads without doing transactional database writes in the request path.
- Durable local spool plus background workers for normalization, PII scrubbing, fingerprinting, facet extraction, and persistence.
- OpenTelemetry-shaped event model with permissive adapters for sender-specific formats.
- Web interface for issue lists, event drill-down, and live event flow.
- First SDKs for TypeScript and Python with default error-handler integration.
- Docker-first deployment with standalone binary support for low-powered hardware.
- Homelab staging/testing deployment through GitHub Actions and K3S.

## Spec Kit Workflow

Use the documents in `specs/001-personal-error-tracker/` as the source of truth:

- `spec.md`: user scenarios and functional requirements
- `plan.md`: technical approach and architecture
- `tasks.md`: implementation backlog
- `contracts/ingest-api.yaml`: initial API contract

## Homelab Runner Scaffold

The `infra/` directory contains a minimal Ansible scaffold for provisioning project-specific GitHub Actions runners. It follows the shape of the adjacent `../rapid-root/infra` setup and expects those shared runner roles to be available locally unless they are vendored later.
