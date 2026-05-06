# Feature Specification: CQRS Read/Write Split

**Feature Branch**: `006-cqrs-read-write-split`
**Created**: 2026-05-06
**Status**: Draft
**Input**: 503 errors during production rollouts due to `Recreate` strategy. Single PVC (ReadWriteOnce) prevents rolling updates. Opportunity to scale reads independently.

## Problem

BugBarn runs as a single pod with `strategy: Recreate` because the SQLite database lives on a `ReadWriteOnce` PVC. During deployments the old pod is killed before the new one starts, causing 503s. With `replicas: 1` and a single PVC, true zero-downtime deploys are impossible.

## Solution

Split the single pod into a **writer** (owns the database) and **reader** (stateless read replicas) using a single binary controlled by `BUGBARN_MODE` env var. Readers restore a SQLite copy from Litestream (S3) and serve all read traffic. Writes are forwarded from readers to the writer via internal HTTP.

## Architecture

```
                    ┌─────────────────────────────────┐
                    │           Ingress                │
                    │  bugbarn.wiebe.xyz/*              │
                    └──────────────┬──────────────────-┘
                                   │
                    ┌──────────────▼──────────────────-┐
                    │     Reader Service (ClusterIP)    │
                    │     bugbarn-reader:8080           │
                    └──────────────┬──────────────────-┘
                                   │
               ┌───────────────────┼───────────────────┐
               │                   │                   │
     ┌─────────▼────────┐ ┌───────▼────────┐ ┌───────▼────────┐
     │  Reader Pod 1     │ │ Reader Pod 2    │ │ Reader Pod N    │
     │  BUGBARN_MODE=    │ │ BUGBARN_MODE=   │ │ BUGBARN_MODE=   │
     │    reader         │ │   reader        │ │   reader        │
     │  emptyDir SQLite  │ │ emptyDir SQLite │ │ emptyDir SQLite │
     │  (restored from   │ │ (restored from  │ │ (restored from  │
     │   Litestream)     │ │  Litestream)    │ │  Litestream)    │
     └─────────┬─────────┘ └───────┬────────┘ └───────┬────────┘
               │                   │                   │
               │         writes forwarded via HTTP     │
               └───────────────────┼───────────────────┘
                                   │
                    ┌──────────────▼──────────────────-┐
                    │     Writer Service (ClusterIP)    │
                    │     bugbarn-writer:8080           │
                    │     (internal only)               │
                    └──────────────┬──────────────────-┘
                                   │
                    ┌──────────────▼──────────────────-┐
                    │        Writer Pod                 │
                    │  BUGBARN_MODE=writer              │
                    │  PVC: bugbarn-data (RWO)          │
                    │  Litestream replicating to S3     │
                    │  Spool + background worker        │
                    └──────────────────────────────────-┘
```

## Modes

The binary supports three modes via `BUGBARN_MODE`:

| Mode | Behavior |
|------|----------|
| _(unset)_ | Current single-pod behavior. Fully backwards compatible. |
| `writer` | Owns PVC, runs spool/worker/Litestream replication. Handles all mutations. Exposes internal forwarding endpoint. |
| `reader` | Stateless. Restores SQLite from Litestream on startup. Serves GET traffic. Forwards non-GET requests to writer via HTTP. |

## Route Classification

### Reader serves directly (from local read-only SQLite)

All `GET` and `OPTIONS` requests:

- `GET /api/v1/health`, `GET /api/v1/runtime-config`
- `GET /api/v1/me`, `GET /api/v1/setup/*`
- `GET /api/v1/openapi.yaml`, `GET /api/docs`
- `GET /api/v1/issues`, `GET /api/v1/issues/sparklines`, `GET /api/v1/issues/{id}`, `GET /api/v1/issues/{id}/events`
- `GET /api/v1/events/{id}`, `GET /api/v1/events/stream`, `GET /api/v1/live/events`
- `GET /api/v1/logs`, `GET /api/v1/logs/stream`
- `GET /api/v1/projects`, `GET /api/v1/projects/pending-count`
- `GET /api/v1/releases`, `GET /api/v1/releases/{id}`
- `GET /api/v1/alerts`, `GET /api/v1/alerts/{id}`
- `GET /api/v1/settings`
- `GET /api/v1/source-maps`
- `GET /api/v1/apikeys`
- `GET /api/v1/facets`, `GET /api/v1/facets/{key}`
- `GET /api/v1/analytics/*`
- `GET /api/v1/groups`, `GET /api/v1/groups/{slug}`
- `GET /analytics.js`

### Reader forwards to writer (via internal HTTP)

All non-GET requests:

- `POST /api/v1/events` — event ingest (idempotent via `ingestId`)
- `POST /api/v1/logs` — log ingest
- `POST /api/v1/analytics/collect` — pageview collection
- `POST /api/v1/issues/{id}/resolve`, `POST /api/v1/issues/{id}/reopen`
- `PATCH /api/v1/issues/{id}/mute`, `PATCH /api/v1/issues/{id}/unmute`
- `POST /api/v1/login`, `POST /api/v1/logout`
- `POST/PUT/DELETE /api/v1/releases/*`
- `POST/PUT/DELETE /api/v1/alerts/*`
- `POST/PUT/DELETE /api/v1/projects/*`
- `POST/PUT/DELETE /api/v1/groups/*`
- `POST /api/v1/source-maps`
- `PUT/POST /api/v1/settings`
- `DELETE /api/v1/apikeys/{id}`

The reader validates auth/session **before** forwarding, rejecting invalid requests early.

## Event Forwarding

Reader pods forward writes to the writer via plain HTTP reverse proxy:

```go
type WriteForwarder struct {
    writerURL string   // "http://bugbarn-writer:8080"
    client    *http.Client
}

func (f *WriteForwarder) Forward(w http.ResponseWriter, r *http.Request) {
    // Copy method, path, query, headers, body → upstream
    // Copy response status, headers, body → downstream
}
```

### Why HTTP forwarding (not gRPC, Redis, NATS)

- Writer already speaks HTTP with the same routes — zero protocol changes
- ~30 lines of Go reverse proxy code
- Events have `ingestId` for idempotency — safe to retry
- No new infrastructure dependencies
- If writer is down, reader returns 502 — acceptable for self-hosted tool

### Failure handling

| Scenario | Behavior |
|----------|----------|
| Writer healthy | Forward succeeds, return writer's response |
| Writer temporarily down | Reader returns 502 to client, client SDK retries (SDKs have built-in retry) |
| Writer slow | 30s timeout for mutations, 10s for ingest, then 504 |
| Network partition | Same as writer down — 502, SDK retries |

Ingest events are idempotent via `ingestId` so retries are safe. For mutations (resolve/mute), the UI can retry or show an error banner.

## Reader SQLite Lifecycle

### Startup

1. Entrypoint runs `litestream restore` to get latest DB snapshot from S3
2. If no replica exists (fresh install), start with empty database
3. Open SQLite in read-only mode
4. Start serving traffic

### Periodic refresh

A background goroutine refreshes the local SQLite every 30-60 seconds:

1. `litestream restore -o /tmp/bugbarn-restore.db`
2. Atomic rename over the serving copy
3. Close and reopen the read-only `*sql.DB` connection

### Consistency model

- Readers are **eventually consistent**, typically 30-60s behind the writer
- This is acceptable for an error tracking dashboard
- "Resolve issue → refresh page" may briefly show stale state
- Mitigated by optimistic UI updates in the SPA (mark as resolved client-side immediately)

## Storage Changes

### `storage.OpenReadOnly(path string) (*Store, error)`

- Opens only the read connection (`roDB`) using read-only SQLite DSN
- Leaves `db` (write connection) as nil — write methods will error clearly
- Skips schema creation and migrations (replica already has schema)
- All existing query methods use `readDB()` and work unchanged

## Configuration

| Env var | Values | Default | Description |
|---------|--------|---------|-------------|
| `BUGBARN_MODE` | `""`, `writer`, `reader` | `""` | Pod operating mode |
| `BUGBARN_WRITER_URL` | URL | — | Writer service URL (required for reader mode) |

## K8s Manifest Changes

### Writer deployment (`writer-deployment.yaml`)

- Current `deployment.yaml` renamed
- `replicas: 1`, `strategy: Recreate`
- Mounts `bugbarn-data` PVC
- Env: `BUGBARN_MODE=writer`
- All existing env vars and secrets unchanged
- Selector: `app.kubernetes.io/component: writer`

### Writer service (`writer-service.yaml`)

- `name: bugbarn-writer`
- ClusterIP (internal only — not exposed via ingress)
- Port 8080

### Reader deployment (`reader-deployment.yaml`)

- `replicas: 2`
- `strategy: RollingUpdate`, `maxUnavailable: 0`, `maxSurge: 1`
- No PVC — uses `emptyDir` for restored SQLite
- Env: `BUGBARN_MODE=reader`, `BUGBARN_WRITER_URL=http://bugbarn-writer:8080`
- Litestream S3 credentials (for restore only)
- No `nodeSelector` — can schedule on any node
- Selector: `app.kubernetes.io/component: reader`

### Reader service

- Keeps name `bugbarn` for ingress compatibility
- Selector updated to `app.kubernetes.io/component: reader`

### Ingress

- No changes — all traffic still goes to `bugbarn` service (now backed by reader pods)
- Readers handle classification and forwarding internally

## Entrypoint Changes

```sh
case "$BUGBARN_MODE" in
  reader)
    litestream restore -config /etc/litestream.yml \
      -if-replica-exists "$BUGBARN_DB_PATH" \
      || echo "No replica found, starting with empty DB."
    exec bugbarn
    ;;
  writer|"")
    exec litestream replicate -config /etc/litestream.yml -exec bugbarn
    ;;
esac
```

## Implementation Phases

### Phase 1 — Config + mode flag
- Add `Mode` and `WriterURL` to `internal/config/config.go`
- Validation: mode must be `""`, `writer`, or `reader`
- No behavior change when unset

### Phase 2 — Read-only storage
- Add `OpenReadOnly()` to `internal/storage/`
- Test that it refuses writes and serves reads

### Phase 3 — Write forwarder
- Implement `internal/api/forwarder.go`
- Plain HTTP reverse proxy, ~30 lines
- Test with httptest

### Phase 4 — Modal startup in main.go
- Branch `run()` on `cfg.Mode`
- Reader: open read-only, skip worker/spool, wire forwarder
- Writer: current behavior
- Legacy (unset): current behavior unchanged

### Phase 5 — Litestream restore loop
- `internal/reader/restore.go` — periodic restore + atomic DB swap
- Handle edge cases: restore failure, empty replica

### Phase 6 — Docker entrypoint
- Mode-aware startup in `entrypoint.sh`

### Phase 7 — K8s manifests
- Writer + reader deployments and services
- Test in staging first
- Migration path: deploy new binary in legacy mode → add writer + reader → update ingress → remove old deployment

## Acceptance Criteria

- [ ] `BUGBARN_MODE` unset: identical to current behavior (backwards compatible)
- [ ] Writer pod handles all writes, runs spool/worker/Litestream
- [ ] Reader pods serve all GET requests from local read-only SQLite
- [ ] Reader pods forward all non-GET requests to writer
- [ ] Reader pods restore from Litestream on startup and refresh every 30-60s
- [ ] Rolling update on reader pods causes zero 503s
- [ ] Event ingest via reader pod reaches writer and persists correctly
- [ ] Issue resolve/reopen via reader pod reaches writer and takes effect
- [ ] Auth validation happens on reader before forwarding (invalid requests rejected early)
- [ ] `go build ./...` and `go test ./...` pass
- [ ] Deploy to staging with writer (1 replica) + readers (2 replicas) and verify
- [ ] Rolling update of reader pods while sending traffic — no dropped requests
