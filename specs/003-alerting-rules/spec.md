# Feature Specification: Alerting Rules — event_count_exceeds

**Feature Branch**: `003-alerting-rules`
**Created**: 2026-04-22
**Spec**: 003-alerting-rules
**Status**: Shipped
**Last verified against the tree**: 2026-10-17 at 70a663f
**Issues**: none found
**Principles touched**: VII (BugBarn reports its own failures)
**Input**: Phase 2 spec (002) defined the alert schema and delivery pipeline but left the `event_count_exceeds` condition unimplemented. This spec closes that gap.

---

## Background

BugBarn's alert infrastructure (schema, CRUD, webhook delivery, Slack/Discord payloads) shipped in Phase 2 and the `new_issue` and `regression` conditions are fully wired. The `event_count_exceeds` condition exists in the database but the evaluator ignores it. Without it, operators cannot be notified when a known issue spikes — they can only be notified the first time it appears or when a resolved issue regresses.

---

## User Story — Operators Are Notified When an Issue Exceeds a Volume Threshold

As a homelab operator, I want to define an alert rule that fires when an issue accumulates more than N events, so I know when a low-severity issue has become high-volume without it needing to be new or regressed.

**Why this priority**: The `new_issue` and `regression` conditions cover introduction and recurrence but not sustained spikes. A background error that fires 500 times overnight needs attention even if it was first seen weeks ago.

**Independent Test**: Create an alert rule with `condition: event_count_exceeds` and `threshold: 5`. Send 6 events with the same fingerprint. Verify an HTTP POST is received at the webhook URL. Send 3 more events. Verify no duplicate POST is received within the cooldown window.

**Acceptance Scenarios**:

1. **Given** an enabled alert rule with `condition: event_count_exceeds` and `threshold: N`, **When** an issue's total event count exceeds N, **Then** BugBarn sends a webhook within 30 seconds of the event being processed.
2. **Given** the alert fired for an issue, **When** further events arrive within the cooldown window, **Then** no duplicate POST is sent.
3. **Given** the cooldown has expired, **When** another event arrives for the same issue, **Then** the alert fires again (because the issue still exceeds the threshold).
4. **Given** an alert rule with `event_count_exceeds` and `threshold: 0`, **Then** the rule is treated as effectively disabled for this condition (threshold must be > 0 to fire).

---

## Implementation

### New domain event

```go
// IssueEventRecorded is published for every successfully persisted event,
// regardless of whether the issue is new or regressed. Used to evaluate
// event_count_exceeds alert conditions.
type IssueEventRecorded struct {
    Issue     storage.Issue
    ProjectID int64
}
```

### Evaluator change

`evaluator.HandleEvent` gains a new case:

```go
case domainevents.IssueEventRecorded:
    e.evaluate(ctx, v.ProjectID, v.Issue, "event_count_exceeds")
```

`evaluate` already handles cooldown checking. For `event_count_exceeds` the threshold guard is added:

```go
if conditionType == "event_count_exceeds" && (rule.Threshold <= 0 || int(issue.EventCount) <= rule.Threshold) {
    continue
}
```

### Service change

`service.PublishIssueEvent` already publishes `IssueCreated` and `IssueRegressed`. It additionally publishes `IssueEventRecorded` for every event (whether new, regressed, or routine):

```go
bus.Publish(domainevents.IssueEventRecorded{Issue: issue, ProjectID: projectID})
```

---

## Requirements

### Functional Requirements

- **FR-301**: Alert rules with `condition: event_count_exceeds` MUST fire when the issue's `event_count` exceeds `threshold`. Issues: none found. Tests: `internal/alert/evaluator_test.go::TestEvaluator_ConditionMatching`.
- **FR-302**: A threshold of 0 or less MUST NOT trigger a firing. [inferred] `internal/alert/evaluator.go` skips a threshold of 0 or less; no test names it.
- **FR-303**: Cooldown enforcement from Phase 2 (per alert/issue pair, configurable `cooldown_minutes`) MUST apply to `event_count_exceeds` identically to other conditions. Issues: none found. Tests: `internal/alert/evaluator_test.go::TestEvaluator_CooldownPreventsDoubleFire`, `::TestEvaluator_CooldownExpiredAllowsFire`.
- **FR-304**: The alert payload MUST include `event_count` in the `issue` block so the receiver can display the current volume. Issues: none found. Tests: `internal/alert/deliverer_test.go::TestDeliverer_GenericPayload`.
- **FR-305**: New-issue email MUST be capped per project: after `newIssueMailMax` (5) in a 10 minute window, further new issues send no email of their own (admin mail and project rules with an email address alike) and are reported in one summary mail when the window ends. Webhook-only rules are not capped. Suppressed issues are counted in `bugbarn.alert.new_issue_suppressed`. Issues: [#241](https://github.com/webwiebe/bugbarn/issues/241). Tests: `internal/alert/new_issue_limit_test.go::TestEvaluator_NewIssueFloodMailsFiveThenOneSummary`, `::TestEvaluator_RuleMailsShareTheNewIssueCap`.

### Non-Functional Requirements

- **NFR-301**: Publishing `IssueEventRecorded` MUST add no observable latency to the worker loop (synchronous publish to in-process bus handlers; handlers spawn their own goroutines for network I/O). [inferred]

---

## Success Criteria

- **SC-301**: An alert with `event_count_exceeds / threshold: 5` fires after the 6th event for a given issue and does not fire again until the cooldown expires.
- **SC-302**: Setting `threshold: 0` produces no firings.
- **SC-303**: The Slack payload for an `event_count_exceeds` firing includes the current event count in the fields block.

---

## Drift

| Claim | Source | Tree |
|---|---|---|
| Status Draft | Header | `event_count_exceeds` is evaluated in `internal/alert/evaluator.go` and offered in the web alert form (`web/src/components/alerts.ts`) |
| Conditions are `new_issue`, `regression`, `event_count_exceeds` | Background | `internal/alert/types.go` also lists `message_contains` |
| `IssueEventRecorded` is published synchronously | NFR-301 | `internal/domainevents/events.go` defines the event; handler goroutine behaviour not established |
| SC-303 shows the count in the Slack fields block | Success Criteria | `TestDeliverer_SlackPayload` covers the Slack shape; a test of the count field specifically does not exist |

## Proof contract

- [ ] A rule with `event_count_exceeds` and `threshold: 5` fires on the 6th event and stays quiet until the cooldown ends.
- [ ] A rule with `threshold: 0` never fires.

## Sources

- `internal/alert/evaluator.go`, `deliverer.go`, `types.go`
- `internal/domainevents/events.go`
