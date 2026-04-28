# Alerting

BugBarn can send a webhook notification whenever a specific condition occurs on an issue. Alerts are configured per project and evaluated in-process as events are ingested.

---

## Condition Types

### `new_issue`

Fires when a brand-new fingerprint is seen for the first time — i.e., when a new issue row is created. It does **not** fire on subsequent events for the same issue.

**Example:** You want to know immediately when a never-before-seen error class appears in production.

---

### `regression`

Fires when a resolved issue receives a new event and transitions to `regressed` status.

**Example:** You resolved a `NullPointerException` last week. If it reappears, you want a Slack notification so the team can triage the relapse.

---

### `event_count_exceeds`

Fires when an issue's cumulative `event_count` crosses the configured `threshold` value. The alert fires once at the crossing point; it does not re-fire on every subsequent event.

**Example:** Set `threshold: 100` to be notified when any single issue has accumulated more than 100 occurrences.

---

## Creating an Alert

### Via the UI

1. Open the project and navigate to **Settings → Alerts**.
2. Click **New alert**.
3. Fill in:
   - **Name** — a human-readable label shown in webhook payloads.
   - **Condition** — select `new_issue`, `regression`, or `event_count_exceeds`.
   - **Threshold** — required for `event_count_exceeds`; ignored otherwise.
   - **Cooldown (minutes)** — minimum time between firings for the same alert/issue pair. The UI suggests 15 minutes.
   - **Webhook URL** — the destination for notifications.
   - **Enabled** — toggle to activate/deactivate without deleting.

### Via the API

```http
POST /api/v1/alerts
Content-Type: application/json

{
  "name": "New production issue",
  "enabled": true,
  "condition": "new_issue",
  "threshold": 0,
  "cooldown_minutes": 15,
  "webhook_url": "https://hooks.slack.com/services/T.../B.../..."
}
```

For `event_count_exceeds`:

```http
POST /api/v1/alerts
Content-Type: application/json

{
  "name": "High-volume issue",
  "enabled": true,
  "condition": "event_count_exceeds",
  "threshold": 100,
  "cooldown_minutes": 60,
  "webhook_url": "https://example.com/hooks/bugbarn"
}
```

---

## Webhook Integrations

The payload format is determined automatically from the webhook URL.

### Slack

Triggered when the URL contains `hooks.slack.com`. Uses [Block Kit](https://api.slack.com/block-kit) with section blocks and an action button.

```json
{
  "text": "[BugBarn] New production issue: TypeError: Cannot read properties of undefined",
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*[BugBarn]*\n`TypeError: Cannot read properties of undefined`"
      }
    },
    {
      "type": "section",
      "fields": [
        {"type": "mrkdwn", "text": "*Severity*\nerror"},
        {"type": "mrkdwn", "text": "*Events*\n42"}
      ]
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": {"type": "plain_text", "text": "View Issue"},
          "url": "https://bugbarn.example.com/issues/issue-000001"
        }
      ]
    }
  ]
}
```

---

### Discord

Triggered when the URL contains `discord.com/api/webhooks`. Uses an [embed](https://discord.com/developers/docs/resources/message#embed-object) with color red (`15158332`).

```json
{
  "content": "[BugBarn] New issue",
  "embeds": [
    {
      "title": "TypeError: Cannot read properties of undefined",
      "description": "TypeError",
      "color": 15158332,
      "fields": [
        {"name": "Severity", "value": "error", "inline": true}
      ],
      "url": "https://bugbarn.example.com/issues/issue-000001"
    }
  ]
}
```

---

### Generic JSON

Used for all other webhook URLs. A plain JSON object is posted.

```json
{
  "alert": "New production issue",
  "condition": "new_issue",
  "project": "42",
  "issue": {
    "id": "issue-000001",
    "title": "TypeError: Cannot read properties of undefined",
    "url": "https://bugbarn.example.com/issues/issue-000001",
    "first_seen": "2026-04-26T10:00:00Z",
    "event_count": 1,
    "severity": "error"
  }
}
```

> **Note:** `severity` is taken from the issue's representative event. If the representative event has no severity set, it defaults to `"error"`.

---

## Cooldown

The cooldown prevents the same alert from firing repeatedly for the same issue in a short window. Each firing is recorded in the `alert_firings` table with the `alert_id`, `issue_id`, and `fired_at` timestamp. Before firing, BugBarn checks whether the last `fired_at` for this `alert_id` + `issue_id` pair is within `cooldown_minutes`. If it is, the alert is silently skipped.

**Timeline example** with `cooldown_minutes: 60`:

```
10:00  Event arrives → condition met → alert fires ✓  (recorded in alert_firings)
10:05  Another event → condition met → last fired 5m ago → skipped (cooldown)
10:30  Another event → condition met → last fired 30m ago → skipped (cooldown)
11:01  Another event → condition met → last fired 61m ago → alert fires ✓
```

A cooldown of `0` means the alert fires on every qualifying event with no suppression.

---

## Retry Behaviour

When a webhook call fails, BugBarn retries up to **3 attempts total** with exponential backoff:

| Attempt | Delay before attempt |
|---------|----------------------|
| 1 | Immediate |
| 2 | 1 second |
| 3 | 2 seconds |

Each attempt has a **5-second per-request timeout**. If all three attempts fail, the error is logged and the firing is not recorded (so the cooldown is not consumed and the alert may fire again on the next qualifying event).

---

## Alert Evaluation

The alert evaluator runs **in-process** — there is no separate alerting service. It is triggered synchronously by domain events published from the background ingest worker:

- After a new issue is created → `new_issue` condition is evaluated
- After a regression is detected → `regression` condition is evaluated
- After every event ingested → `event_count_exceeds` condition is evaluated against the issue's updated `event_count`

The webhook delivery itself is performed asynchronously so that a slow or unavailable webhook destination does not block event ingestion.

---

## API Reference

### `GET /api/v1/alerts`

List all alerts for the current project.

**Response**

```json
[
  {
    "id": "alert-000001",
    "name": "New production issue",
    "enabled": true,
    "condition": "new_issue",
    "threshold": 0,
    "cooldown_minutes": 15,
    "webhook_url": "https://hooks.slack.com/...",
    "created_at": "2026-04-01T00:00:00Z",
    "updated_at": "2026-04-01T00:00:00Z"
  }
]
```

---

### `POST /api/v1/alerts`

Create a new alert. See [Creating an Alert](#creating-an-alert) for request body fields.

---

### `GET /api/v1/alerts/{id}`

Return a single alert by ID.

---

### `PATCH /api/v1/alerts/{id}`

Update an alert. Accepts any subset of the fields accepted by `POST`.

---

### `DELETE /api/v1/alerts/{id}`

Delete an alert. Existing `alert_firings` rows for this alert are retained for audit purposes but the alert will no longer fire.
