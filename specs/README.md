# Specs

One spec per product area. Each spec states the behaviour BugBarn ships, links
every functional requirement to the issue that asked for it and the test that
proves it, and records in its `## Drift` section where a document or issue
disagrees with the code. The code is the fact.

The principles every spec answers to are in the
[constitution](../.specify/memory/constitution.md). The templates are in
[`.specify/templates/`](../.specify/templates/).

All specs were last verified against the tree on 2026-10-17 at `70a663f`.

## Index

| Spec | Status | What it covers |
|---|---|---|
| [001 Personal error tracker](./001-personal-error-tracker/spec.md) | Shipped | Ingest, the spool and worker, normalization, privacy scrubbing, fingerprint grouping, issues and events, the API and web UI, auth, the SDKs, and the delivery pipeline. |
| [002 Phase 2 and 3 improvements](./002-phase2-improvements/spec.md) | Partial | Alert rules and delivery, muting, the hourly sparkline, user context, breadcrumbs, the Go SDK and self-reporting. |
| [003 Alerting rules](./003-alerting-rules/spec.md) | Shipped | The `event_count_exceeds` condition, its threshold and its cooldown. |
| [004 Weekly digest](./004-weekly-digest/spec.md) | Shipped | The scheduled digest over webhook and email, its period, top issues and time budgets. |
| [005 UI fixes and project management](./005-ui-fixes-project-management/spec.md) | Partial | Narrow-screen layout, the issues list, deleting projects, aliases, merges and groups. Rejecting a pending project has no route. Its [tasks](./005-ui-fixes-project-management/tasks.md) are the original checklist. |
| [006 CQRS read/write split](./006-cqrs-read-write-split/spec.md) | Shipped | The writer and reader modes, read-only storage and write forwarding. The Litestream restore it describes is gone. |
| [007 Redis write queue](./007-redis-write-queue/spec.md) | Partial | The Redis queue between readers and the writer. Phase 5, retiring the HTTP drain, is open. |

## Workflow

1. **A feature starts as a spec.** Before code, write a new
   `specs/NNN-<feature>/spec.md` from the spec template, or change the spec of
   the area it belongs to. The issue links to that spec. The issue's "What" and
   "Why" become the Purpose, and its acceptance criteria become acceptance
   scenarios and FR-NNN lines.
2. **Refinement fills the plan and the tasks.** The issue's "Implementation
   Details" and "Execution Plan" go into `plan.md`, including the Constitution
   Check. The issue's checklist goes into `tasks.md`, one task per line, each
   naming the FR it serves. Small changes can skip `plan.md` and `tasks.md` when
   the issue already holds them.
3. **A PR that changes behaviour updates its spec.** The same PR adds or edits
   the FR lines, links the new tests, moves resolved items out of Open
   Questions, and adds or clears Drift rows. A reviewer can then check each
   requirement against a test.
4. **Unbacked statements are marked.** A requirement with no issue and no test
   carries `[inferred]` until one of them exists. A requirement the code does not
   meet carries `[gap]` and an issue.
5. **Drift is recorded, then fixed.** When a document and the code disagree,
   add a Drift row and treat the code as the fact. Fix the document, or open an
   issue when the code is the thing that is wrong.

Numbers are never reused or renumbered. A retired area keeps its folder with
`Status: Deferred` or `Superseded` and a pointer to what replaced it. Results
and load-test output belong in `docs/`, not under a spec number.

## Related documents

- [CLAUDE.md](../CLAUDE.md) and [AGENTS.md](../AGENTS.md): working rules for this repository
- [docs/](../docs/): operator and SDK documentation
- [deploy/CLAUDE.md](../deploy/CLAUDE.md): environments, secrets and the release chain
