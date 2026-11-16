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
- **Merge**: One project per product (#282): `staging`, `testing`, `e2e` and `dev` are an environment on the data (`deployment.environment`), never a project. A merge moves the source project's rows to the target and stamps the environment the caller names (default: the source slug suffix, else `production`) on the moved issues that carry none: issues, events, facets, analytics pageviews, alerts, releases, source maps, logs and held events. An issue whose fingerprint the target already has folds into it (events re-pointed, counts added, first and last seen widened); the other issues are renumbered after the target's own. Rows that would collide with the target's own (daily analytics, monitors) stay behind, and the target keeps its own settings. `projectTables` in `internal/storage/project_tables.go` is the one list that merge and delete both read (#270). The source row is kept and archived (`status = archived`, `merged_into`, `merged_environment`): it leaves the project list, its ingest keys and its slug keep working and resolve to the target with the environment stamped on events that name none. Each merge writes a `project_merges` row with the rows moved per table and the issue-number offset, listed by `GET /api/v1/project-merges` and `bb projects --merges`. Nothing is deleted. Creating or renaming a project to a name that ends in an environment (`-prod`, `-staging`, `-test`, `-e2e`, `-dev`) is refused.
- **Groups**: A parent entity (`project_groups`) that provides combined views across member projects. Example: a "bugbarn" group containing `bugbarn-service`, `bugbarn-web`, `bugbarn-site` — view all errors at a glance.

**Acceptance Scenarios**:

1. **Given** two active projects, **When** the admin merges them, **Then** issues and events from the source project are moved to the target stamped with the environment, the source row is archived and the merge is recorded.
2. **Given** a merged project, **When** an SDK sends events to the old slug or with a key of the source, **Then** they are ingested into the target project with the environment of the merge, with no change at the sender.
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
- **FR-507**: Merging a project MUST move its data to the target stamped with an environment, archive the source instead of deleting it, keep the source's slug and ingest keys resolving to the target plus that environment, fold issues that share a fingerprint, and record the counts. Issues: [#282](https://github.com/webwiebe/bugbarn/issues/282). Tests: `internal/storage/projects_test.go::TestMergeProjectsMovesDataAndCreatesAlias`, `internal/storage/project_merge_test.go::TestMergeProjectsMovesAPIKeysAndReleases`, `internal/storage/project_merge_env_test.go::TestMergeFoldsCollidingIssuesAndRenumbersTheRest`, `::TestMergeRecordsAuditAndRefusesAnArchivedSource`, `internal/storage/project_tables_test.go`, `cmd/bb/cmds_resources_test.go::TestProjectsMergeIntoAnEnvironment`.
- **FR-513**: A project name or slug that ends in an environment word (`prod`, `production`, `stage`, `staging`, `test`, `testing`, `e2e`, `dev`, `development`, after `-` or `_`) MUST be refused on create and rename with `400`. Issues: [#282](https://github.com/webwiebe/bugbarn/issues/282). Tests: `internal/storage/project_merge_env_test.go::TestCreateRefusesEnvironmentSuffixedNames`, `internal/domain/environment_test.go::TestEnvironmentSuffix`.
- **FR-514**: An event that names no environment and arrives through an archived source's key or slug MUST be stored with the merge's environment as `deployment.environment`. The `environment` attribute the SDKs send counts as the environment facet. Issues: [#282](https://github.com/webwiebe/bugbarn/issues/282). Tests: `internal/ingestproc/stamp_environment_test.go::TestStampEnvironmentOnlyFillsAMissingOne`.
- **FR-508**: Project groups MUST exist as a parent entity with member projects. Issues: none found. Tests: `internal/storage/projects_test.go::TestGroupCRUD`.
- **FR-509**: Selecting a group in the project dropdown MUST aggregate issues, analytics and alerts across its members. [inferred]
- **FR-510**: The issue detail page MUST stay readable below 600px: the fingerprint truncates, JSON blocks scroll horizontally and action buttons wrap. [inferred]
- **FR-511**: A project MUST be able to carry a FunnelBarn URL, set in Settings through `PUT /api/v1/projects/:slug/funnelbarn`. The value is empty or an absolute http(s) URL without credentials; `{surface}` in it is replaced per row. The "By surface" table on the Issues page links each row to it and shows no link column when the setting is empty. BugBarn never calls FunnelBarn. Issues: [#222](https://github.com/webwiebe/bugbarn/issues/222). Tests: `internal/api/project_funnelbarn_test.go::TestProjectFunnelBarnURLIsSetListedAndCleared`, `::TestProjectFunnelBarnURLRejectsUnsafeValues`, `internal/service/projects/funnelbarn_test.go`, `web/scripts/surfaces.test.mjs`.
- **FR-512**: The project, group and alias endpoints MUST be ServeMux patterns (`DELETE /api/v1/projects/{slug}`, `POST /api/v1/projects/{slug}/merge`, ...) registered in `internal/api/routes_protected.go`. A method a path does not offer answers 405 with an `Allow` header once the caller is authenticated, and an unauthenticated request to any protected path answers 401 first, so an anonymous caller learns nothing about which paths exist. Routing errors are plain text. Issues: none found. Tests: `internal/api/routes_characterization_test.go::TestRoutesCharacterization`, `internal/api/openapi_routes_test.go::TestServedOperationsAreInOpenAPISpec`.

---

## Drift

| Claim | Source | Tree |
|---|---|---|
| Status Draft; every task in `tasks.md` unchecked | Header, tasks.md | Delete, rename, merge, aliases and groups ship (`internal/api/routes_protected.go`, `internal/storage/projects.go`, `groups.go`) |
| `POST /api/v1/projects/:slug/reject` | tasks.md T008 | Retired in #228. `DELETE` removes the project row, its held ingest payloads and its key requests, and the settings Reject button calls it. A reject route would add no record, because nothing reads one. Ingest for the same slug afterwards creates a new pending project |
| Delete of an active project with issues requires confirmation | Story 3, scenario 3 | Confirmation is a UI concern; not established from the API |
| Project, group and alias routes matched by path prefix and an in-handler method check (`dispatchProtected`) | Earlier routing in `routes_protected.go` | Replaced by ServeMux patterns with `r.PathValue`. A wrong method on a known path answers 405 with `Allow` where it answered 404, ServeMux redirects unclean paths, and `/api/v1/projects/{slug}/` still serves DELETE. Behavior otherwise unchanged; `routes_characterization.tsv` pins it |
| Merge deletes the source and turns its slug into an alias | Story 4, FR-507 before #282 | Merge archives the source row and routes its slug and keys to the target (`followMerge`, `ValidAPIKeySHA256`); no alias row is written and nothing is deleted |
| Layout fixes verified at 375, 600, 1120 and 1440px | tasks.md T003 | Not established |

## Proof contract

- [ ] Deleting a project removes its issues, events and keys.
- [ ] Events sent to a renamed or merged slug land in the target project.
- [ ] The issues list for an empty project returns `[]`.

## Sources

- `internal/api/routes_protected.go`, `internal/api/projects.go`
- `internal/storage/projects.go`, `internal/storage/groups.go`
- `web/styles.css`
