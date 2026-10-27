# Feature Specification: UI Fixes & Project Management

**Feature Branch**: `005-ui-fixes-project-management`
**Created**: 2026-05-05
**Spec**: 005-ui-fixes-project-management
**Status**: Partial
**Last verified against the tree**: 2026-10-17 at 70a663f
**Issues**: none found
**Principles touched**: none
**Input**: Layout bugs on narrow screens, empty issues list, missing project delete/reject, project merging/linking.

## User Scenarios & Testing

### User Story 1 — Settings Page Layout Breaks at 1120px (Priority: P1)

As a user on a narrow screen or resized browser, I want the settings page to remain usable, so I can configure BugBarn without content being clipped.

**Root cause**: At `@media (max-width: 1120px)`, `.app-frame` collapses to `grid-template-columns: 1fr` but sidebar content (form inputs, SDK install URLs, tarball paths) overflows its grid cell with no `overflow` or `min-width: 0` constraint.

**Acceptance Scenarios**:

1. **Given** a viewport width between 600px and 1120px, **When** the settings page is rendered, **Then** all form inputs, labels, and SDK install instructions are fully visible without horizontal clipping.
2. **Given** a viewport width below 600px, **When** the settings page is rendered, **Then** content wraps or stacks vertically and remains scrollable.

### User Story 2 — Issues List Returns Empty (Priority: P1)

As a user, I want to see my issues when I open the issues page, so I can triage errors.

**Likely causes**:
- API returns `"issues": null` instead of `"issues": []` (nil-slice serialization)
- Project filter context sends `project_id=0` after the recent middleware fix, or the "All projects" view doesn't omit the project filter

**Acceptance Scenarios**:

1. **Given** issues exist across projects, **When** the user selects "All projects" and views the issues list, **Then** all issues are returned.
2. **Given** a project with no issues, **When** the user views that project's issues, **Then** an empty list is shown (not an error or null).
3. **Given** the API is called with `?limit=50`, **When** issues exist, **Then** the response contains `"issues": [...]` (never `null`).

### User Story 3 — Delete or Reject Pending Projects (Priority: P2)

As an admin, I want to delete or reject pending projects (e.g. created by SDK typos like "funnelbarni"), so I can keep my project list clean.

**Acceptance Scenarios**:

1. **Given** a project with status "pending", **When** the admin clicks "Reject" in settings, **Then** the project and any associated data are deleted.
2. **Given** a project with status "active" and no issues/events, **When** the admin clicks "Delete", **Then** the project is removed.
3. **Given** a project with status "active" and existing issues, **When** the admin attempts to delete, **Then** a confirmation is required warning about data loss.

### User Story 4 — Merge or Link Related Projects (Priority: P3)

As a user with multiple projects representing the same application (e.g. `funnelbarn` + `funnelbarn-site`, or `bugbarn-service` + `bugbarn-site` + `bugbarn-web`), I want to group them so I can see a combined view.

**Decision**: Aliases + groups. No client-side config changes needed when renaming or merging.

- **Aliases**: When a project is renamed or merged, the old slug becomes an alias. SDKs sending to the old slug are transparently routed to the target project. Stored in a `project_aliases` table.
- **Merge**: Moves all issues, events, analytics, and alerts from source project to target. Source slug becomes an alias. Source project is deleted.
- **Groups**: A parent entity (`project_groups`) that provides combined views across member projects. Example: a "bugbarn" group containing `bugbarn-service`, `bugbarn-web`, `bugbarn-site` — view all errors at a glance.

**Acceptance Scenarios**:

1. **Given** two active projects, **When** the admin merges them, **Then** issues and events from the source project are moved to the target, the source slug becomes an alias, and the source project is deleted.
2. **Given** a merged project, **When** an SDK sends events to the old slug, **Then** they are ingested into the target project transparently.
3. **Given** a project is renamed, **When** an SDK sends events to the old slug, **Then** they are routed to the renamed project via the alias.
4. **Given** a group containing three projects, **When** the admin views the group, **Then** issues from all member projects are shown in a combined list.
5. **Given** a group, **When** selected in the project dropdown, **Then** analytics, issues, and alerts aggregate across all member projects.

### User Story 5 — Issue Detail Page Content Cut Off Below 600px (Priority: P1)

As a mobile user, I want the issue detail page to be readable on small screens, so I can triage issues from my phone.

**Root cause**: The detail page has fixed-width elements (fingerprint hash, JSON blocks, action buttons) that overflow the viewport without wrapping or horizontal scroll.

**Acceptance Scenarios**:

1. **Given** a viewport width below 600px, **When** viewing an issue detail page, **Then** the fingerprint hash truncates with ellipsis, JSON blocks scroll horizontally, and action buttons wrap to remain accessible.
2. **Given** a viewport width below 600px, **When** viewing the event occurrence chart, **Then** the chart scales to fit the viewport.

---

## Requirements

### Functional Requirements

- **FR-501**: The settings page MUST stay readable between 375px and 1120px viewport width without clipped inputs, install URLs or tarball paths. Issues: none found. [inferred] Styles live in `web/styles.css`; no test covers layout.
- **FR-502**: The issues API MUST return `"issues": []` for an empty result. Issues: none found. Tests: `internal/api/issues_test.go::TestListIssuesPagination`.
- **FR-503**: The "All projects" view MUST list issues from every project. [inferred]
- **FR-504**: An admin MUST be able to delete a project with its related data through `DELETE /api/v1/projects/:slug`. Issues: none found. Tests: `internal/storage/projects_test.go::TestDeleteProjectWithRelatedData`.
- **FR-505**: A pending project MUST be rejectable. Rejecting is `DELETE /api/v1/projects/:slug`; there is no `/reject` route (#228). Issues: [#228](https://github.com/webwiebe/bugbarn/issues/228). Tests: `internal/storage/project_reject_test.go::TestDeletePendingProjectRejectsIt`.
- **FR-506**: Renaming a project MUST keep the old slug as an alias that routes events to the renamed project. Issues: none found. Tests: `internal/storage/projects_test.go::TestRenameProjectCreatesAlias`, `::TestEnsureProjectResolvesAlias`.
- **FR-507**: Merging a project MUST move its data to the target, turn the source slug into an alias and delete the source. Issues: none found. Tests: `internal/storage/projects_test.go::TestMergeProjectsMovesDataAndCreatesAlias`.
- **FR-508**: Project groups MUST exist as a parent entity with member projects. Issues: none found. Tests: `internal/storage/projects_test.go::TestGroupCRUD`.
- **FR-509**: Selecting a group in the project dropdown MUST aggregate issues, analytics and alerts across its members. [inferred]
- **FR-510**: The issue detail page MUST stay readable below 600px: the fingerprint truncates, JSON blocks scroll horizontally and action buttons wrap. [inferred]
- **FR-511**: A project MUST be able to carry a FunnelBarn URL, set in Settings through `PUT /api/v1/projects/:slug/funnelbarn`. The value is empty or an absolute http(s) URL without credentials; `{surface}` in it is replaced per row. The "By surface" table on the Issues page links each row to it and shows no link column when the setting is empty. BugBarn never calls FunnelBarn. Issues: [#222](https://github.com/webwiebe/bugbarn/issues/222). Tests: `internal/api/project_funnelbarn_test.go::TestProjectFunnelBarnURLIsSetListedAndCleared`, `::TestProjectFunnelBarnURLRejectsUnsafeValues`, `internal/service/projects/funnelbarn_test.go`, `web/scripts/surfaces.test.mjs`.

---

## Drift

| Claim | Source | Tree |
|---|---|---|
| Status Draft; every task in `tasks.md` unchecked | Header, tasks.md | Delete, rename, merge, aliases and groups ship (`internal/api/routes.go`, `internal/storage/projects.go`, `groups.go`) |
| `POST /api/v1/projects/:slug/reject` | tasks.md T008 | Retired in #228. `DELETE` removes the project row, its held ingest payloads and its key requests, and the settings Reject button calls it. A reject route would add no record, because nothing reads one. Ingest for the same slug afterwards creates a new pending project |
| Delete of an active project with issues requires confirmation | Story 3, scenario 3 | Confirmation is a UI concern; not established from the API |
| Layout fixes verified at 375, 600, 1120 and 1440px | tasks.md T003 | Not established |

## Proof contract

- [ ] Deleting a project removes its issues, events and keys.
- [ ] Events sent to a renamed or merged slug land in the target project.
- [ ] The issues list for an empty project returns `[]`.

## Sources

- `internal/api/routes.go`, `internal/api/projects.go`
- `internal/storage/projects.go`, `internal/storage/groups.go`
- `web/styles.css`
