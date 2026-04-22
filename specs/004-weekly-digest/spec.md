# Feature Specification: Weekly Digest

**Feature Branch**: `004-weekly-digest`
**Created**: 2026-04-22
**Status**: Draft
**Input**: Operators want a scheduled summary of error trends so they can review the week's health without opening the UI every day.

---

## Background

BugBarn's alert system fires immediately on individual events. Operators also need a periodic summary: what was new this week, what spiked, what was resolved. Without a digest, low-severity issues accumulate silently and weekly reviews require manually browsing the issue list.

---

## User Story — Operators Receive a Weekly Error Summary

As a homelab operator, I want BugBarn to send me a weekly summary of my project's error activity — new issues, top issues by volume, and resolutions — so I can track trends and catch slow-burning problems without checking the dashboard daily.

**Why this priority**: Immediate alerts catch spikes but miss the "slow burn" pattern. A weekly digest gives operational visibility with a single glance and naturally generates a release of BugBarn (one less lonely week for Barnaby).

**Independent Test**: Set `BUGBARN_DIGEST_WEBHOOK_URL` to a request bin. Wait for (or manually trigger) the digest run. Verify a POST is received with the correct shape and that issue counts match what the database holds for the past 7 days.

**Acceptance Scenarios**:

1. **Given** `BUGBARN_DIGEST_WEBHOOK_URL` is set, **When** the configured digest day/hour arrives, **Then** BugBarn POSTs a JSON summary to the URL with issue counts, top errors, and a period timestamp.
2. **Given** SMTP is configured (`BUGBARN_SMTP_HOST` + `BUGBARN_DIGEST_TO`), **When** the digest fires, **Then** BugBarn sends an HTML email to the configured address.
3. **Given** both webhook and email are configured, **When** the digest fires, **Then** both are delivered independently (a failure in one does not suppress the other).
4. **Given** neither webhook URL nor SMTP is configured, **When** the service starts, **Then** no digest scheduler goroutine is launched — behaviour is identical to today.
5. **Given** the digest fires and the project has zero events this week, **Then** the digest is still sent with zero counts (not skipped).
6. **Given** `BUGBARN_DIGEST_DAY=1` and `BUGBARN_DIGEST_HOUR=8`, **When** it is Monday at 08:00 local time, **Then** the digest fires within one minute.

---

## Digest Content

**Period**: last 7 calendar days (UTC midnight to now at send time).

| Field | Description |
|---|---|
| `period_start` | ISO-8601 UTC timestamp of the start of the 7-day window |
| `period_end` | ISO-8601 UTC timestamp of send time |
| `total_events` | Total events ingested in the period |
| `new_issues` | Count of issues first seen in the period |
| `resolved_issues` | Count of issues resolved in the period |
| `regressions` | Count of issues that regressed in the period |
| `top_issues` | Up to 5 issues with the most events in the period (id, title, event_count, status, url) |

---

## Configuration

All digest configuration is via environment variables.

| Env Var | Default | Description |
|---|---|---|
| `BUGBARN_DIGEST_DAY` | `0` (Sunday) | Weekday to send digest (0=Sun … 6=Sat) |
| `BUGBARN_DIGEST_HOUR` | `8` | Hour (0–23, UTC) to send digest |
| `BUGBARN_DIGEST_WEBHOOK_URL` | — | Webhook URL to POST digest JSON to |
| `BUGBARN_DIGEST_TO` | — | Email recipient address |
| `BUGBARN_SMTP_HOST` | — | SMTP server hostname |
| `BUGBARN_SMTP_PORT` | `587` | SMTP server port |
| `BUGBARN_SMTP_USER` | — | SMTP username |
| `BUGBARN_SMTP_PASS` | — | SMTP password |
| `BUGBARN_SMTP_FROM` | — | From address (defaults to `BUGBARN_SMTP_USER` if unset) |

---

## Webhook Payload

```json
{
  "type": "weekly_digest",
  "period_start": "2026-04-15T00:00:00Z",
  "period_end": "2026-04-22T08:00:00Z",
  "project": "my-app",
  "stats": {
    "total_events": 1842,
    "new_issues": 3,
    "resolved_issues": 7,
    "regressions": 1
  },
  "top_issues": [
    {
      "id": "42",
      "title": "TypeError: cannot read properties of null",
      "event_count": 934,
      "status": "unresolved",
      "url": "https://bugbarn.example.com/#/issues/42"
    }
  ]
}
```

---

## Email Format

Plain-text fallback + HTML multipart. The HTML part uses inline styles only (no external CSS). Subject: `[BugBarn] Weekly digest — {project} — {period}`.

```
Subject: [BugBarn] Weekly digest — my-app — Apr 15–22 2026

This week in my-app (Apr 15 – Apr 22 UTC):

  1 842 events   3 new issues   7 resolved   1 regression

Top issues by volume:
  #42  TypeError: cannot read properties of null  (934 events, unresolved)
  #17  ReferenceError: x is not defined            (288 events, unresolved)

View all issues: https://bugbarn.example.com
```

---

## Implementation

### New package: `internal/digest`

```
internal/digest/
  digest.go      — DigestData struct and Gather(ctx, store, projectID, since) function
  scheduler.go   — StartScheduler(ctx, cfg, store) goroutine; fires at configured day/hour
  mailer.go      — SendEmail(ctx, cfg, data) via net/smtp
  webhook.go     — SendWebhook(ctx, url, data) via net/http
```

### New storage method

```go
// WeeklyDigest returns aggregate stats and top issues for the given project
// since the given time.
func (s *Store) WeeklyDigest(ctx context.Context, projectID int64, since time.Time) (storage.DigestData, error)
```

```go
type DigestData struct {
    TotalEvents     int
    NewIssues       int
    ResolvedIssues  int
    Regressions     int
    TopIssues       []DigestIssue
}

type DigestIssue struct {
    ID         string
    Title      string
    EventCount int
    Status     string
}
```

### Scheduler

The scheduler goroutine wakes every minute, checks if it is the configured weekday+hour (UTC), and fires the digest if it hasn't already fired within the past 23 hours (to survive minor timing drift across restarts).

---

## Requirements

### Functional Requirements

- **FR-401**: When `BUGBARN_DIGEST_WEBHOOK_URL` is set, the digest MUST be POSTed as JSON to that URL every configured day/hour.
- **FR-402**: When SMTP is configured and `BUGBARN_DIGEST_TO` is set, the digest MUST be sent as an email (HTML + plain text multipart) every configured day/hour.
- **FR-403**: Digest delivery failures MUST be logged and MUST NOT crash the service.
- **FR-404**: When neither channel is configured, the scheduler goroutine MUST NOT start.
- **FR-405**: The digest MUST cover the 7 days preceding the send time.
- **FR-406**: `top_issues` MUST contain at most 5 entries ordered by event count descending within the period.

### Non-Functional Requirements

- **NFR-401**: Storage queries for digest data MUST NOT hold the database connection for more than 2 seconds; a context deadline of 10 seconds is applied.
- **NFR-402**: Email and webhook delivery MUST each have a 15-second timeout.
- **NFR-403**: The scheduler MUST add no observable CPU overhead between fire times (sleeps until next minute check).

---

## Success Criteria

- **SC-401**: Setting `BUGBARN_DIGEST_WEBHOOK_URL` to a request bin and waiting for the configured day/hour produces a JSON POST with `type: "weekly_digest"` and correct counts.
- **SC-402**: An email is received with correct subject line and issue counts when SMTP is configured.
- **SC-403**: With zero events this week, the digest still sends with `total_events: 0` and an empty `top_issues` array.
- **SC-404**: With neither channel configured, `grep "digest" <(journalctl -u bugbarn)` returns nothing on startup.
