# BugBarn Architecture

This document describes the repository layout and the current runtime flow for the personal error tracker foundation.

## Runtime Flow

SDK/client -> ingest API -> durable spool -> background worker -> normalization/privacy/fingerprinting -> SQLite storage -> query API/web

1. A client or SDK posts an event to `POST /api/v1/events` with the BugBarn API key header.
2. The ingest handler authenticates the request, enforces body limits, and appends the raw payload plus request metadata to the durable local spool.
3. `cmd/bugbarn` keeps the service running and starts the background worker loop alongside the HTTP server.
4. The worker reads spool records, decodes the payload, normalizes it into the canonical event shape, scrubs sensitive values, and derives a fingerprint.
5. The service layer applies issue lifecycle and live-window rules, then calls repository interfaces for persistence.
6. Storage repositories persist the processed issue, event, and facet rows in SQLite.
7. The API server serves read endpoints for the UI, accepts source map artifact uploads, and the browser client renders issue lists, issue detail, event detail, and recent live events from those read APIs.

## Repository Layout

- `cmd/bugbarn`: process entrypoint. It loads config, opens storage, opens the spool, starts the HTTP server, and runs the background worker. `worker-once` is the maintenance path for processing the current spool one time.
- `internal/ingest`: request-path ingest handler. It owns API key validation, size checks, and durable enqueue into the spool.
- `internal/api`: HTTP server and route dispatch. It owns request/response handling and delegates use cases to `internal/service`.
- `internal/service`: business use cases and error boundary between HTTP handlers and repositories.
- `internal/worker`: spool record processing. It owns decoding, normalization, privacy scrubbing, and fingerprint generation before persistence.
- `internal/storage`: SQLite repository implementation, schema setup, migrations, inserts, issue grouping, event persistence, facet storage, and read queries.
- `internal/normalize`, `internal/privacy`, `internal/fingerprint`: transformation helpers used by the worker before data reaches storage.
- `web`: browser UI for issue and event inspection.
- `sdks/typescript`, `sdks/python`: client SDKs.
- `examples`: local validation programs and the load generator.
- `specs/001-personal-error-tracker`: the source-of-truth product spec, plan, tasks, fixtures, and ingest contract.

## Ownership Boundaries

- `cmd/bugbarn` wires the process together; it does not own route semantics or storage policy.
- `internal/api` owns the HTTP server surface, including `GET /api/v1/issues`, `GET /api/v1/issues/{id}`, `GET /api/v1/issues/{id}/events`, `GET /api/v1/events/{id}`, and `GET /api/v1/live/events`. Handlers should stay limited to parsing, authentication/session handling, response formatting, and calling service methods.
- `internal/service` owns business behavior such as issue resolution/reopen, regression semantics, live-event recency windows, releases, alerts, settings, and source-map upload use cases.
- `internal/storage` implements repository interfaces. SQL and migration details stay behind this boundary and should not leak to API handlers or frontend responses.
- `internal/ingest` owns `POST /api/v1/events`.
- `internal/worker` owns the transformation pipeline from raw spool record to processed event.
- `internal/storage` owns the SQLite representation and query behavior.

## Endpoint Definitions

- The public ingest contract lives in [`specs/001-personal-error-tracker/contracts/ingest-api.yaml`](../specs/001-personal-error-tracker/contracts/ingest-api.yaml).
- The source map upload contract lives in [`specs/001-personal-error-tracker/contracts/source-maps-api.yaml`](../specs/001-personal-error-tracker/contracts/source-maps-api.yaml).
- The HTTP route map is implemented in [`internal/api/server.go`](../internal/api/server.go).
- The browser UI expects the read/live endpoints documented in [`web/README.md`](../web/README.md).

## Source Language Notes

- Browser-side scripting source should be authored in TypeScript.
- Any JavaScript committed for the browser should be treated as build output or temporary legacy source, not the preferred authoring format.
- The foundation SDKs are TypeScript and Python. PHP is reserved for a later SDK iteration.
