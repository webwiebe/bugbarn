# Feature Specification: UI Fixes & Project Management

**Feature Branch**: `005-ui-fixes-project-management`
**Created**: 2026-05-05
**Status**: Draft
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
