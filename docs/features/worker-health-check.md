# Worker Health Check

## Problem

The `/api/v1/health` endpoint returns a static `{"status":"ok"}` that only
proves the HTTP server is running. It says nothing about whether the background
worker is actually processing events.

In a recent production incident, events were ingested successfully (the spool
accepted them and returned 202), but issues never appeared in the UI. The worker
was running but the associated projects were in `pending` status. There was no
signal — no metric, no health degradation, no alert — that the pipeline was
effectively broken.

The health check should catch:

1. **Worker stall** — the spool cursor hasn't advanced despite pending records.
2. **Dead-letter buildup** — too many records are failing processing.
3. **Project approval gap** — events are landing for pending projects.

## Desired Behavior

### Enhanced `/api/v1/health` response

```json
{
  "status": "ok",
  "worker": {
    "healthy": true,
    "lastAdvance": "2026-05-01T17:00:00Z",
    "pendingRecords": 0,
    "deadLetterCount": 12,
    "staleSince": null
  },
  "projects": {
    "pendingCount": 0,
    "pendingWithEvents": []
  }
}
```

### Health status rules

| Condition | Status |
|-----------|--------|
| Worker advanced cursor in last 5 minutes OR no pending records | `ok` |
| Worker has not advanced in 5+ minutes with pending records | `degraded` |
| Worker has not advanced in 15+ minutes with pending records | `unhealthy` |
| Dead-letter count increased by 10+ in last hour | `degraded` |
| Any project with `status: pending` has received events | `degraded` |

The top-level `status` field is the worst of all sub-checks:
`ok` < `degraded` < `unhealthy`.

### Worker metrics (in-process)

The background worker in `cmd/bugbarn/main.go` should track:

- `lastCursorAdvance time.Time` — updated each time the cursor moves forward.
- `pendingRecords int` — count of records ahead of the cursor in the active
  spool file.
- `deadLetterCount int` — total dead-lettered records (read from disk on
  startup, incremented in-memory).
- `processedTotal int64` — total records processed since startup (useful for
  rate monitoring).

These are exposed via a `WorkerStatus` struct that the health handler reads.

### Structured log on stall

When the worker detects it has pending records but hasn't advanced in 5 minutes,
emit a structured log:

```
level=warn msg="worker stall detected" pending=3 last_advance=2026-05-01T16:55:00Z
```

This ensures the stall shows up in BugBarn's own log stream when self-reporting
is enabled, creating a visible signal even without external monitoring.

### Self-reporting integration

When the worker transitions from `ok` to `degraded` or `unhealthy`, and
self-reporting is enabled, capture a BugBarn event:

```go
bb.CaptureMessage(fmt.Sprintf("worker health degraded: %d pending, last advance %s ago", pending, sinceAdvance))
```

This makes the health degradation appear as an issue in BugBarn itself.

## Implementation

### New files

- `internal/worker/status.go` — `WorkerStatus` struct with atomic fields,
  `Snapshot() HealthReport` method.

### Changed files

- `cmd/bugbarn/main.go` — pass `*WorkerStatus` to `runBackgroundWorker`;
  update fields on cursor advance, dead-letter, and stall detection.
- `internal/api/server.go` — add `workerStatus *worker.WorkerStatus` field,
  `SetWorkerStatus(*worker.WorkerStatus)` setter.
- `internal/api/health.go` (new or inline in server.go) — enhanced health
  handler that merges HTTP-level "ok" with worker status and project status.
- `internal/storage/projects.go` — add `PendingProjectsWithEvents(ctx) ([]string, error)`
  query that joins projects (status=pending) with events table.

### Wire-up in main.go

```go
ws := &worker.WorkerStatus{}
apiServer.SetWorkerStatus(ws)
go runBackgroundWorker(ctx, eventSpool, spoolDir, store, svc, selfReporting, ws)
```

## Monitoring integration

The enhanced health endpoint is compatible with Kubernetes liveness/readiness
probes:

- **Liveness**: `GET /api/v1/health` — returns 200 as long as the process is
  up (current behavior, unchanged).
- **Readiness**: `GET /api/v1/health?detail=true` — returns 200 for `ok`,
  503 for `degraded`/`unhealthy`. K8s readiness probes using this will stop
  routing traffic to pods with a stalled worker.

The `?detail=true` parameter gates the extended response to avoid breaking
existing monitoring that expects the simple `{"status":"ok"}` format.
