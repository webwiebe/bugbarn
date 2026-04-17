# Tasks: Phase 2 & 3 Improvements

**Input**: Design documents from `/specs/002-phase2-improvements/`

## Phase 2A: Alert Delivery

- [ ] T101 Add `webhook_url`, `condition`, `threshold`, `cooldown_minutes`, and `last_fired_at` columns to the alerts schema via `ensureColumn` migrations.
- [ ] T102 Add `alert_firings` table (`alert_id`, `issue_id`, `fired_at`) to track per-rule cooldown state.
- [ ] T103 Implement alert condition evaluation after each successful issue upsert in the worker: check `new_issue`, `regression`, and `event_count_exceeds`.
- [ ] T104 Implement webhook delivery: generic JSON payload with 5 s timeout, 3-attempt exponential backoff, fire-and-forget goroutine.
- [ ] T105 Implement Slack Block Kit payload shape auto-detected by `hooks.slack.com` host.
- [ ] T106 Implement Discord embed payload shape auto-detected by `discord.com/api/webhooks` host.
- [ ] T107 Add `BUGBARN_PUBLIC_URL` env var; include issue deep-link in alert payloads when set.
- [ ] T108 Update alert create/update UI form to expose `webhook_url`, `condition`, `threshold`, and `cooldown_minutes` fields.
- [ ] T109 Add tests: alert condition evaluation, cooldown enforcement, payload shape for generic/Slack/Discord targets.

## Phase 2B: Issue Mute

- [ ] T110 Add `mute_mode` column (`until_regression` | `forever` | empty) to issues schema via `ensureColumn`.
- [ ] T111 Add `muted` and `regressed` to valid issue status values; update `upsertIssue` regression path to check mute mode before setting `regressed`.
- [ ] T112 Add `PATCH /api/v1/issues/:id/mute` endpoint accepting `{"mute_mode": "until_regression"|"forever"}`.
- [ ] T113 Update issue list default filter to show `unresolved` and `regressed` (exclude `muted`); add "Muted" option to status filter dropdown.
- [ ] T114 Add Mute button with mode dropdown to issue detail UI alongside the Resolve button.
- [ ] T115 Add tests: mute transitions, `until_regression` re-emergence, `forever` suppression, unmute.

## Phase 2C: Issue Sparklines

- [ ] T116 Add `hourly_counts` query to `ListIssuesFiltered`: single `GROUP BY` over `events` for all issues in the result set, returning 24-integer arrays keyed by issue ID.
- [ ] T117 Include `hourly_counts` in `GET /api/v1/issues` response per issue.
- [ ] T118 Render 24-bar CSS sparkline in each issue row in the web UI (flex row, `height` proportional to count, no JS charting library).
- [ ] T119 Add tests: sparkline query correctness, empty-hours handling, proportional scaling.

## Phase 3A: User Context

- [ ] T120 Add `UserContext` struct (`id`, `email`, `username`) to `internal/event/event.go`.
- [ ] T121 Extend normalizer to extract `user` from event payload into `event.User`.
- [ ] T122 Add `user_json` column to `events` table; persist and retrieve `UserContext` as JSON.
- [ ] T123 Index `user.id` as a facet key in `insertFacets` so issues can be filtered by `?user.id=X`.
- [ ] T124 Add `setUser` / `clearUser` to TypeScript SDK; attach to envelope on capture.
- [ ] T125 Add `set_user` / `clear_user` to Python SDK; attach to envelope on capture.
- [ ] T126 Add `setUser` / `clearUser` to PHP SDK (`Client::setUser`/`Client::clearUser`).
- [ ] T127 Render User section in event detail UI when `user` is present (id, email, username).
- [ ] T128 Add tests: user context round-trip (ingest → store → API), `user.id` facet indexing, SDK setUser/clearUser.

## Phase 3B: Breadcrumbs

- [ ] T129 Add `Breadcrumb` struct and `Breadcrumbs []Breadcrumb` to `internal/event/event.go`.
- [ ] T130 Extend normalizer to extract `breadcrumbs` array from event payload.
- [ ] T131 Add `breadcrumbs_json` column to `events` table; persist and retrieve as JSON.
- [ ] T132 Implement TypeScript SDK breadcrumb ring buffer (100-entry cap, circular).
- [ ] T133 Implement TypeScript SDK `console` auto-interceptor (log/warn/error/info/debug → category `console`).
- [ ] T134 Implement TypeScript SDK `fetch` and `XMLHttpRequest` auto-interceptor (category `http`, method, url, status_code).
- [ ] T135 Implement TypeScript SDK navigation auto-interceptor (`hashchange`, `pushState`, `replaceState` → category `navigation`, from/to).
- [ ] T136 Add `autoBreadcrumbs` init option (default `true`); interceptors installed lazily and idempotently.
- [ ] T137 Add manual `addBreadcrumb` API to TypeScript, Python, and PHP SDKs.
- [ ] T138 Add `clearBreadcrumbs` API to all three SDKs.
- [ ] T139 Render Breadcrumbs timeline section in event detail UI above the stacktrace.
- [ ] T140 Add tests: ring buffer cap, interceptor categories, manual add, breadcrumb round-trip.

## Phase 2D: Dogfooding — Go SDK and Self-Reporting

- [ ] T141 Create `sdks/go/` Go module with `Init`, `CaptureError`, `CaptureMessage`, `Flush`, `Shutdown` matching the Python SDK transport model (buffered channel, background goroutine).
- [ ] T142 Implement `RecoverMiddleware(next http.Handler) http.Handler` in the Go SDK for panic capture.
- [ ] T143 Add `WithAttributes`, `WithUser` capture options to the Go SDK.
- [ ] T144 Add Go SDK tests: transport queue cap, flush timeout, panic recovery, pre-init no-op.
- [ ] T145 Add Go SDK sample app (`sdks/go/sample/main.go`) demonstrating `Init`, `CaptureError`, and `RecoverMiddleware`.
- [ ] T146 Add `BUGBARN_SELF_ENDPOINT`, `BUGBARN_SELF_API_KEY`, `BUGBARN_SELF_PROJECT` env vars to `cmd/bugbarn/main.go`; initialize Go SDK when set.
- [ ] T147 Wrap the HTTP `ServeHTTP` with `bugbarn.RecoverMiddleware` when self-reporting is enabled.
- [ ] T148 Call `bugbarn.CaptureMessage` from the worker dead-letter path with ingest ID and last error.
- [ ] T149 Call `bugbarn.Shutdown(2s)` in the service graceful shutdown path.
- [ ] T150 Document self-reporting setup in `docs/operations.md` (create project, create ingest-only key, set env vars).
- [ ] T151 Add CI step for Go SDK: `go test ./...`, `go vet ./...`, `go build ./...`.
