# Feature Specification: Personal Error Tracker Foundation

**Feature Branch**: `001-personal-error-tracker`  
**Created**: 2026-04-15  
**Status**: Draft  
**Input**: Build a barebones, open-source, self-hosted Sentry-like system for personal use with fast non-blocking ingest, OpenTelemetry-based event normalization, deduplicated errors/issues, live event views, lightweight auth, SDKs for TypeScript and Python, Docker/binary deployment, and homelab CI/CD.

## User Scenarios & Testing

### User Story 1 - Applications Can Send Errors Without Waiting on Storage (Priority: P1)

As an application owner, I want my services to send high volumes of errors to the tracker without being slowed down by database writes, so error reporting never becomes an outage amplifier.

**Why this priority**: The ingest path is the core product risk. If it blocks on storage, the project fails its primary purpose.

**Independent Test**: Run a local load test against the ingest endpoint while the database is stopped or slow; authenticated requests are accepted into the durable ingest buffer until configured backpressure limits are reached.

**Acceptance Scenarios**:

1. **Given** a valid application API key, **When** an application posts an OpenTelemetry-shaped exception event, **Then** the service authenticates the request and returns an accepted response after durable enqueue, without waiting for issue/event database rows.
2. **Given** a burst of valid events, **When** workers cannot persist them as fast as they arrive, **Then** the ingest service continues accepting events until the configured local spool limit and then returns explicit retryable backpressure responses.
3. **Given** malformed but parseable JSON, **When** required canonical fields are missing, **Then** the service creates a best-effort event with parse diagnostics rather than rejecting the payload unless authentication or size limits fail.

### User Story 2 - Repeated Events Collapse Into Issues (Priority: P1)

As a user, I want repeated occurrences of the same error to appear as one issue with many events, so I can focus on unique problems instead of duplicate noise.

**Why this priority**: Issue grouping is the main difference between a log stream and an error tracker.

**Independent Test**: Submit events with the same exception type/message/normalized stack trace but different timestamps, IDs, IPs, and request paths; verify one issue is created with multiple events.

**Acceptance Scenarios**:

1. **Given** two events with the same normalized exception and stack trace, **When** workers process them, **Then** both events reference the same issue.
2. **Given** volatile tokens such as UUIDs, numeric IDs, IP addresses, timestamps, and hex addresses inside messages or paths, **When** a fingerprint is generated, **Then** those volatile tokens are stripped or generalized before hashing.
3. **Given** a new fingerprint, **When** the first matching event is processed, **Then** a new issue is created with first-seen, last-seen, and occurrence count metadata.

### User Story 3 - Users Can Investigate Issues and Events (Priority: P2)

As a user, I want a web interface that lists issues, shows their frequency and recency, and lets me move through the linked events, so I can understand what is breaking and where.

**Why this priority**: The product must be usable without direct database access.

**Independent Test**: Seed issues and events through the ingest API, open the web UI, filter/sort the issue list, open an issue, and navigate between its events.

**Acceptance Scenarios**:

1. **Given** processed issues, **When** the user opens the issue list, **Then** issues are sorted by a useful default such as most recent or highest frequency and include counts, first seen, last seen, project, severity, and selected facets.
2. **Given** an issue with multiple events, **When** the user opens the issue detail page, **Then** they can inspect normalized exception data, scrubbed raw context, facets, and previous/next events.
3. **Given** stored event context, **When** it appears in the UI, **Then** sensitive values have already been removed or redacted.

### User Story 4 - Users Can Watch Live Event Flow (Priority: P2)

As a user, I want a live view of events currently flowing through the system, so I can see whether a deploy or incident is actively producing errors.

**Why this priority**: Real-time feedback makes the self-hosted tool useful during debugging and deployment.

**Independent Test**: Open the live events page, submit events from a test SDK, and verify new events appear without page refresh.

**Acceptance Scenarios**:

1. **Given** the live page is open, **When** new events are accepted and processed, **Then** the UI appends them in near real time.
2. **Given** events include runtime context such as host, release, environment, user agent, status code, or route, **When** the live view renders, **Then** it shows useful non-PII context for each event.
3. **Given** live updates are unavailable, **When** the connection drops, **Then** the UI recovers through reconnect or polling without losing the ability to inspect persisted events.

### User Story 5 - Applications Integrate Through SDKs (Priority: P2)

As a developer, I want TypeScript and Python SDKs that can become the default error handlers, so I can add reporting with minimal application code.

**Why this priority**: SDKs determine whether the system is practical outside manual API calls.

**Independent Test**: Install each SDK in a small sample app, configure an endpoint and API key, throw an uncaught exception, and verify the event reaches the ingest service.

**Acceptance Scenarios**:

1. **Given** a TypeScript Node.js app, **When** the SDK is initialized, **Then** uncaught exceptions and unhandled promise rejections are captured and sent asynchronously.
2. **Given** a Python app, **When** the SDK is initialized, **Then** uncaught exceptions are captured through `sys.excepthook` and can also be reported manually.
3. **Given** the ingest endpoint is unavailable, **When** an SDK captures an error, **Then** it fails quietly from the application perspective and does not block process shutdown longer than a configurable timeout.

### User Story 6 - Operators Can Configure Access and Deploy Simply (Priority: P3)

As a self-hosting operator, I want simple application API keys, a local user login, Docker images, and a standalone binary path, so I can run the system on a small server without managing a large platform.

**Why this priority**: The project is intended for personal infrastructure, not a managed SaaS environment.

**Independent Test**: Start the system with Docker Compose or a binary plus environment variables, create or configure an admin user and project API key, and log in to view events.

**Acceptance Scenarios**:

1. **Given** a fresh deployment, **When** `BUGBARN_ADMIN_USERNAME` and `BUGBARN_ADMIN_PASSWORD` or a CLI setup command is provided, **Then** an initial admin user can log in.
2. **Given** an application API key, **When** events are submitted, **Then** they are associated with the correct project/application.
3. **Given** no valid API key, **When** an application submits an event, **Then** the ingest service rejects it before enqueueing.

## Requirements

### Functional Requirements

- **FR-001**: The system MUST expose an authenticated ingest endpoint for application events.
- **FR-002**: The ingest endpoint MUST durably enqueue accepted events before responding and MUST NOT perform issue/event database inserts in the request path.
- **FR-003**: The system MUST define a canonical event envelope based on OpenTelemetry concepts for exceptions, logs, resources, attributes, traces, and severity.
- **FR-004**: The system MUST preserve unknown sender-provided fields in scrubbed raw context.
- **FR-005**: The system MUST reject unauthenticated ingest requests.
- **FR-006**: The system MUST enforce configurable request size and spool size limits.
- **FR-007**: Background workers MUST normalize queued payloads into canonical events.
- **FR-008**: Background workers MUST scrub PII and secrets before persistence and UI exposure.
- **FR-009**: Background workers MUST fingerprint events into issues using normalized exception type, normalized message, stack frames, and selected stable context.
- **FR-010**: Fingerprinting MUST remove volatile identifiers such as UUIDs, IP addresses, timestamps, large numeric IDs, memory addresses, and obvious random tokens.
- **FR-011**: The data model MUST represent an issue/error separately from its event occurrences.
- **FR-012**: Events MUST reference their grouped issue when grouping succeeds.
- **FR-013**: The system MUST track first seen, last seen, occurrence count, project/application, severity, and representative event metadata per issue.
- **FR-014**: The system MUST support flexible facets extracted from event context, including but not limited to host, runtime, release, environment, route, status code, user agent family, and region.
- **FR-015**: The system MUST keep a registry of discovered facet keys and maintain queryable facet values.
- **FR-016**: The API MUST provide issue list, issue detail, event list, event detail, and facet filtering endpoints.
- **FR-017**: The web UI MUST include an issue list view.
- **FR-018**: The web UI MUST include an issue detail view with event navigation.
- **FR-019**: The web UI MUST include a live event flow view.
- **FR-020**: The system MUST support username/password login for human users.
- **FR-021**: Initial user credentials MUST be configurable through environment variables and/or CLI setup.
- **FR-022**: The system MUST provide project/application API key management.
- **FR-023**: The project MUST ship Docker images for ingest/API/worker and web components.
- **FR-024**: The ingest/API/worker service SHOULD build as a standalone binary.
- **FR-025**: The project MUST provide TypeScript and Python SDKs before the first public release.
- **FR-026**: SDKs MUST support default uncaught error handlers and manual capture.
- **FR-027**: SDKs MUST send events asynchronously and avoid blocking application execution on network failures.
- **FR-028**: CI MUST run tests, linting, builds, and container image checks.
- **FR-029**: CI/CD MUST support testing and staging namespaces in the homelab K3S cluster.

### Non-Functional Requirements

- **NFR-001**: Ingest p95 response time under local durable enqueue SHOULD stay below 10 ms on modest hardware during normal operation.
- **NFR-002**: A single-node deployment SHOULD tolerate bursts of at least 1,000 small events per second on development hardware, with exact targets refined through benchmarks.
- **NFR-003**: The default deployment MUST run within resource budgets suitable for Raspberry Pi-class hardware.
- **NFR-004**: The system MUST degrade through explicit backpressure rather than unbounded memory growth.
- **NFR-005**: The system MUST be operable without external SaaS dependencies.
- **NFR-006**: Stored payloads MUST be scrubbed of known PII and secrets by default.
- **NFR-007**: The product MUST prefer simple, inspectable operational components over distributed-system complexity.

### Key Entities

- **User**: Human who logs into the web UI and administers projects/API keys.
- **Project/Application**: Source system that owns API keys and groups incoming events.
- **API Key**: Secret token used by applications to authenticate ingest requests.
- **Raw Ingest Record**: Durably queued accepted payload before worker processing.
- **Event**: One occurrence of an error or signal after normalization and scrubbing.
- **Issue**: Deduplicated error grouping that aggregates many events sharing a fingerprint.
- **Fingerprint**: Stable hash input derived from normalized exception and context.
- **Facet Key**: Queryable context field discovered from event attributes.
- **Facet Value**: Normalized value for a facet key linked to events/issues.
- **Live Event Cursor**: Stream position used by UI clients to receive recent events.

## Success Criteria

- **SC-001**: A local sample app can send 100,000 authenticated events without request-path database inserts.
- **SC-002**: Duplicate events with volatile tokens collapse into one issue in fixture tests.
- **SC-003**: PII scrubbing tests prove representative emails, raw IPs, tokens, cookies, authorization headers, and session identifiers are not persisted in cleartext.
- **SC-004**: The web UI lets a user find an issue, inspect linked events, and watch live events from a seeded local environment.
- **SC-005**: TypeScript and Python sample apps capture uncaught errors through SDK default handlers.
- **SC-006**: Docker Compose starts a usable single-node deployment with one command and documented environment variables.
- **SC-007**: GitHub Actions can build and test the project on self-hosted runners and deploy to testing/staging K3S namespaces.

## Assumptions

- The project is single-tenant for early releases.
- Personal-use authentication can be simple but must not store plaintext passwords or API keys.
- The canonical event model should be compatible with OpenTelemetry concepts without requiring every SDK to send native OTLP immediately.
- Exact storage choices may evolve, but the request path must remain storage-decoupled.
- Production deployment, multi-user roles, alerting, source maps, and advanced stack trace symbolication are out of scope for the first foundation spec unless added later.
