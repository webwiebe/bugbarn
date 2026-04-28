# Weekly Digest

The weekly digest is a scheduled summary of error activity across all projects delivered via email and/or a webhook. It is designed to give an at-a-glance health report without requiring daily logins.

---

## What the Digest Contains

The digest covers a **7-day rolling window** ending at the moment the digest fires.

### Per-Project Statistics

For each project that had at least one event in the period:

| Metric | Definition |
|--------|-----------|
| `total_events` | All events ingested in the period |
| `new_issues` | Issues whose `first_seen` falls within the period |
| `resolved_issues` | Issues whose `resolved_at` falls within the period |
| `regressions` | Issues whose `last_regressed_at` falls within the period |

Projects with `total_events = 0` for the period are **silently skipped** and do not appear in the digest.

### Top Issues

Up to **5 issues** ordered by `event_count` descending for the period are included per project. Each entry shows the issue title, event count, and current status. When `BUGBARN_PUBLIC_URL` is configured, a direct link to the issue is included.

### Aggregate Summary

The email includes a cross-project totals row (sum of all per-project stats) at the top of the message.

---

## Scheduling

The digest scheduler runs as a goroutine inside the BugBarn process. It uses a **1-minute ticker** and fires when two conditions are simultaneously true:

1. The current UTC weekday matches `BUGBARN_DIGEST_DAY`
2. The current UTC hour matches `BUGBARN_DIGEST_HOUR`

### Re-fire Guard

To prevent the 1-minute ticker from firing the digest many times within the same hour, a guard checks that at least **23 hours** have elapsed since the last successful fire. This means the digest fires once per week even if the process is restarted within the target window.

### Configuration

| Environment Variable | Type | Default | Description |
|----------------------|------|---------|-------------|
| `BUGBARN_DIGEST_ENABLED` | bool | — | Set to `true` to enable the scheduler |
| `BUGBARN_DIGEST_DAY` | int | — | Day of week to send (0 = Sunday, 1 = Monday … 6 = Saturday) |
| `BUGBARN_DIGEST_HOUR` | int | `8` | UTC hour to send (0–23) |
| `BUGBARN_DIGEST_TO` | string | — | Recipient email address |
| `BUGBARN_DIGEST_WEBHOOK_URL` | string | — | Webhook URL to POST the JSON payload to |

The scheduler is a no-op if neither email (`BUGBARN_DIGEST_ENABLED=true` with SMTP configured and `BUGBARN_DIGEST_TO` set) nor `BUGBARN_DIGEST_WEBHOOK_URL` is active.

---

## Email Format

The email is sent as `multipart/alternative` with both a **plain text** and an **HTML** part. Email clients that support HTML will render the HTML; others fall back to plain text.

### Subject Line

```
[BugBarn] Weekly digest — Apr 19–Apr 26 2026
```

Format: `[BugBarn] Weekly digest — {start}–{end}` where start and end are formatted as `Jan 2` and `Jan 2 2006` respectively.

### Plain Text Part

```
BugBarn weekly digest (Apr 19 – Apr 26 UTC)

All projects: 1234 events   17 new issues   8 resolved   3 regressions

── my-app ──
  980 events   12 new   6 resolved   2 regressions

  Top issues:
  · TypeError: Cannot read properties of undefined  (312 events, unresolved) — https://bugbarn.example.com/#/issues/issue-000001
  · SyntaxError: Unexpected token  (88 events, regressed) — https://bugbarn.example.com/#/issues/issue-000004

── payments-service ──
  254 events   5 new   2 resolved   1 regressions
```

### HTML Part

The HTML part renders the same content with:

- A summary table at the top with colour-coded cells: grey for events, amber for new issues, green for resolved, red for regressions.
- Per-project headings with a stats line.
- A table of top issues with clickable titles (when `BUGBARN_PUBLIC_URL` is set).
- A "View all issues →" link at the bottom.

---

## Webhook Payload

When `BUGBARN_DIGEST_WEBHOOK_URL` is set, a POST request is made with `Content-Type: application/json`. The webhook has a **15-second timeout**; there is no retry on failure.

```json
{
  "type": "weekly_digest",
  "period_start": "2026-04-19T08:00:00Z",
  "period_end": "2026-04-26T08:00:00Z",
  "public_url": "https://bugbarn.example.com",
  "projects": [
    {
      "project": "my-app",
      "stats": {
        "total_events": 980,
        "new_issues": 12,
        "resolved_issues": 6,
        "regressions": 2
      },
      "top_issues": [
        {
          "id": "issue-000001",
          "title": "TypeError: Cannot read properties of undefined",
          "event_count": 312,
          "status": "unresolved",
          "url": "https://bugbarn.example.com/#/issues/issue-000001"
        },
        {
          "id": "issue-000004",
          "title": "SyntaxError: Unexpected token",
          "event_count": 88,
          "status": "regressed",
          "url": "https://bugbarn.example.com/#/issues/issue-000004"
        }
      ]
    }
  ]
}
```

`url` is omitted from each issue when `BUGBARN_PUBLIC_URL` is not configured. `public_url` is omitted from the root object for the same reason.

---

## SMTP Configuration

All SMTP settings are supplied via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `SMTP_HOST` | — | SMTP server hostname (required for email delivery) |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_USER` | — | SMTP authentication username |
| `SMTP_PASS` | — | SMTP authentication password |
| `SMTP_FROM` | falls back to `SMTP_USER` | From address in the email envelope |
| `BUGBARN_DIGEST_TO` | — | Recipient address |

BugBarn uses `smtp.PlainAuth` for authentication and sends the MIME message constructed in-process. No external library beyond the Go standard library is used.

**Minimum configuration for email delivery:**

```shell
BUGBARN_DIGEST_ENABLED=true
BUGBARN_DIGEST_TO=ops@example.com
BUGBARN_DIGEST_DAY=1          # Monday
BUGBARN_DIGEST_HOUR=8         # 08:00 UTC
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USER=bugbarn@example.com
SMTP_PASS=secret
```

---

## Transient SMTP Retry

If delivery fails with a transient network error, BugBarn retries up to **3 attempts**:

| Attempt | Delay |
|---------|-------|
| 1 | Immediate |
| 2 | 1 second |
| 3 | 3 seconds |

Retries only occur for errors whose message contains one of: `ETIMEDOUT`, `ECONNREFUSED`, `ENOTFOUND`, `connection reset`, `broken pipe`, `socket`, or `i/o timeout`. Authentication failures, permanent SMTP rejections, and other non-transient errors cause an immediate failure without retrying.

---

## Testing the Digest

There is no built-in HTTP endpoint to trigger a manual digest send. To test:

1. Ensure at least one project has events ingested.
2. Set `BUGBARN_DIGEST_DAY` and `BUGBARN_DIGEST_HOUR` to the current UTC weekday and hour.
3. Restart the process.
4. The scheduler will fire within the next minute.
5. After confirming delivery, restore the original values.

> **Tip:** Watch the process logs for `digest: sending weekly digest` and `digest: sent successfully` (or error lines) to confirm the send was attempted.
