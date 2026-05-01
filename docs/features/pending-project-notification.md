# Pending Project Notification

## Problem

When a new project is created via the setup endpoint (`/api/v1/setup/{slug}`),
it starts with `status: pending`. Events are accepted and ingested, but the
project won't appear in the UI project selector and issues won't be associated
correctly until an admin approves it via `POST /api/v1/projects/{slug}/approve`.

There is no notification anywhere in the UI that pending projects exist. The
only way to discover them is to visit Settings → Projects manually. This caused
a production incident where dogfooding events were silently ingested for days
without issues appearing.

## Desired Behavior

### 1. Global notification banner

When one or more projects have `status: pending`, show a persistent notification
banner at the top of the main layout (below the nav, above page content):

```
⚠ {N} project(s) awaiting approval: {slug1}, {slug2} — [Review in Settings]
```

- The banner links to the Settings page (projects section).
- It appears on every page (issues, logs, live, analytics, settings).
- It dismisses automatically once all projects are approved.
- It does not appear if the user is not authenticated.

### 2. Nav badge

Add a small badge/dot indicator on the Settings nav item when pending projects
exist, similar to how unread counts work in messaging apps. This provides a
subtle persistent hint even after the banner is dismissed.

### 3. Auto-approve option

Add a server config flag `BUGBARN_AUTO_APPROVE_PROJECTS=true` that makes
`EnsureProjectPending` use `EnsureProject` instead (creates with `active`
status). This is useful for single-tenant or trusted deployments where the
approval step is friction without benefit.

## API Changes

### `GET /api/v1/projects`

Already returns all projects with their status. The frontend needs to check for
`status: pending` entries on load and after project mutations.

### Optional: `GET /api/v1/projects/pending-count`

Lightweight endpoint returning `{"count": N}` for the banner, avoiding the need
to fetch the full project list on every page load. Requires auth.

## Implementation Notes

- The frontend already renders pending status with approve buttons in
  `web/src/components.ts:779`. The banner reuses the same data source.
- The projects list is fetched as part of the settings page load
  (`/api/v1/settings`). For the global banner, fetch
  `/api/v1/projects/pending-count` on app init (after auth check).
- The `BUGBARN_AUTO_APPROVE_PROJECTS` flag should be wired in `cmd/bugbarn/main.go`
  and passed to a new `SetAutoApproveProjects(bool)` on the store or through
  the setup handler.

## Files to Change

- `internal/storage/projects.go` — add `PendingProjectCount(ctx) (int, error)`
- `internal/api/server.go` — route `/api/v1/projects/pending-count`
- `internal/api/projects.go` — handler for pending count endpoint
- `internal/api/setup.go` — respect auto-approve flag
- `cmd/bugbarn/main.go` — read `BUGBARN_AUTO_APPROVE_PROJECTS` env var
- `web/src/app.ts` — fetch pending count on init, render banner
- `web/src/components.ts` — banner component, nav badge
- `web/styles.css` — banner and badge styling
