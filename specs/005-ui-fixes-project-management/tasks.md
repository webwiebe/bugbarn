# Tasks: UI Fixes & Project Management

**Input**: Design documents from `/specs/005-ui-fixes-project-management/`

## Phase 1: Layout Fixes (P1)

- [ ] T001 Fix settings page layout at `@media (max-width: 1120px)` — add `min-width: 0` and `overflow-wrap: break-word` to sidebar content cells so form inputs, URLs, and install commands don't clip.
- [ ] T002 Fix issue detail page layout below 600px — fingerprint hash gets `text-overflow: ellipsis`, JSON/stable-context blocks get `overflow-x: auto`, action buttons wrap with `flex-wrap`.
- [ ] T003 Verify both fixes across 375px, 600px, 1120px, and 1440px viewpoints.

## Phase 2: Issues List Bug (P1)

- [ ] T004 Investigate why issues list returns empty — check nil-slice serialization in `ListIssues`/`ListIssuesFiltered`, verify project filter context with "All projects" selected.
- [ ] T005 Ensure `ListIssues` and `ListIssuesFiltered` return `[]Issue{}` instead of `nil` when no results.
- [ ] T006 Verify frontend handles both `null` and `[]` gracefully for the issues array.

## Phase 3: Project Delete/Reject (P2)

- [ ] T007 Add `DELETE /api/v1/projects/:slug` endpoint — deletes project and cascades to issues, events, analytics, alerts, API keys.
- [ ] T008 Add `POST /api/v1/projects/:slug/reject` endpoint — same as delete but only for pending projects.
- [ ] T009 Add "Reject" button to pending project rows in settings UI.
- [ ] T010 Add "Delete" button to active project rows (with confirmation dialog for projects with data).
- [ ] T011 Add service and storage tests for project deletion cascade.

## Phase 4: Project Aliases, Merging & Groups (P3)

**Decision**: Aliases + groups. Renaming/merging a project creates an alias so SDKs don't need config changes. Groups provide combined views across related projects.

- [ ] T012 Add `project_aliases` table: `alias_slug TEXT UNIQUE → project_id`. When an event arrives for an alias slug, route to the target project.
- [ ] T013 Add merge endpoint `POST /api/v1/projects/:slug/merge` — moves all issues/events/analytics from source to target, converts source slug to alias, deletes source project.
- [ ] T014 Add rename endpoint `PUT /api/v1/projects/:slug` — updates name/slug, creates alias from old slug to new project.
- [ ] T015 Update `EnsureProject`/`EnsureProjectPending` to check aliases before creating new projects.
- [ ] T016 Add `project_groups` table: `id, name, slug`. Add `group_id` column to `projects`. Groups aggregate data from member projects.
- [ ] T017 Add group CRUD endpoints and UI — create group, assign projects to group, view group-level issues/analytics.
- [ ] T018 Add tests for alias routing, merge data integrity, and group aggregation.

## Verification

1. `go build ./...` — compiles cleanly
2. `go test ./...` — all tests pass
3. Manual: resize browser to 375px, 600px, 1120px and verify settings + issue detail pages
4. Manual: create a pending project, reject it, verify it's gone
5. Manual: verify issues list shows data with "All projects" selected
6. `bb issues` — verify CLI also shows correct data
