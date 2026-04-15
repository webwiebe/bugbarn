# Research: Personal Error Tracker Foundation

## Decisions

### Ingest Service Language

**Decision**: Use Go for ingest, worker, and API in the first implementation.

**Rationale**: Go gives low memory overhead, straightforward static binaries, stable HTTP primitives, easy concurrency, and a good operational fit for Raspberry Pi-class hosts.

**Alternatives Considered**:

- Python: faster to prototype but weaker for high-throughput ingest and standalone binary distribution.
- TypeScript/Node.js: strong SDK and web ecosystem fit but less attractive for low-resource always-on ingest.

### Request Path Durability

**Decision**: Use a local append-only disk spool in the request path, then process records asynchronously.

**Rationale**: The system needs fast acceptance without database dependency. A disk spool is simpler than requiring a broker and keeps the single-node deployment lightweight.

**Alternatives Considered**:

- Direct database insert: rejected by the constitution and root requirement.
- Redis/NATS/Kafka: useful later but too much operational cost for the default personal deployment.
- In-memory queue only: too risky because accepted events can be lost on process crash.

### Canonical Event Shape

**Decision**: Model events around OpenTelemetry concepts: resource, scope, severity, body, attributes, exception fields, trace/span IDs, and observed timestamp.

**Rationale**: OpenTelemetry is widely understood and lets external systems send structured telemetry without this project inventing a completely custom model.

**Alternatives Considered**:

- Sentry envelope compatibility first: useful future adapter, but too broad for the initial foundation.
- Fully custom JSON only: simpler initially but creates SDK and ecosystem lock-in.

### Storage Direction

**Decision**: Keep the first design storage-adapter friendly, with SQLite as the likely local default and optional Postgres compatibility behind repository interfaces.

**Rationale**: SQLite fits low-powered single-node deployment. Postgres remains useful for homelab staging, testing, and future multi-container deployments.

**Alternatives Considered**:

- Postgres-only: operationally heavier than needed for personal use.
- Badger/Bolt-only: good embedded stores but weaker for relational issue/event/facet queries.

### Frontend Direction

**Decision**: Use a lightweight TypeScript-authored browser UI with server-provided API contracts. Candidate stacks are SvelteKit or Vite + React; choose during implementation based on repo constraints and UI complexity. Browser-side source should stay in TypeScript, with emitted JavaScript treated as build output.

**Rationale**: The UI is data-heavy and interactive but does not need a heavyweight app platform.

**Alternatives Considered**:

- Server-rendered templates only: simplest, but live event flow and rich filtering become less pleasant.
- Next.js: capable, but heavier than the project needs by default.
