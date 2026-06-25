import { escapeAttr, escapeHtml, errorMessage, formatTime } from "../format.js";
import type { ApiAlert } from "../types.js";
import { renderField } from "./shared.js";

export function renderAlertsViewMarkup(alerts: ApiAlert[], error: unknown = null): string {
  return `
    <div class="view-head">
      <h2>Alert rules</h2>
      <span class="chip">${escapeHtml(String(alerts.length))}</span>
    </div>
    <div class="detail-main">
      <div class="section">
        <h3>Configured alerts</h3>
        ${error ? `<div class="error">Unable to load alerts. ${escapeHtml(errorMessage(error))}</div>` : renderAlertList(alerts)}
      </div>
      <div class="section">
        <h3>Create alert</h3>
        <p class="muted">Alerts fire when a condition is met. Deliver via webhook (Slack/Discord auto-detected) or email — set one or both.</p>
        <form class="form-grid" id="alert-form">
          ${renderField("Name", "name", "text", "New errors on checkout")}
          <label class="field">
            <span>Condition</span>
            <select name="condition" id="alert-condition-select">
              <option value="new_issue">New issue created</option>
              <option value="regression">Issue regressed</option>
              <option value="event_count_exceeds">Event count exceeds</option>
              <option value="message_contains">Message contains</option>
            </select>
          </label>
          <label class="field alert-threshold-field" hidden>
            <span>Threshold (event count)</span>
            <input name="threshold" type="number" min="1" placeholder="100" />
          </label>
          <label class="field alert-param-field" hidden>
            <span>Match text (case-insensitive)</span>
            <input name="param" type="text" placeholder="database error" />
          </label>
          ${renderField("Webhook URL", "webhook_url", "url", "https://hooks.slack.com/…")}
          ${renderField("Email", "email_to", "email", "you@example.com")}
          ${renderField("Cooldown (minutes)", "cooldown_minutes", "number", "60")}
          <label class="field field-wide checkbox-field">
            <input name="enabled" type="checkbox" checked />
            <span>Enabled</span>
          </label>
          <div class="link-row form-actions">
            <button type="submit">Create alert</button>
          </div>
        </form>
      </div>
    </div>
  `;
}

function webhookBadge(webhookUrl: string | undefined): string {
  if (!webhookUrl) return "";
  if (webhookUrl.includes("hooks.slack.com")) return `<span class="chip chip-slack">Slack</span>`;
  if (webhookUrl.includes("discord.com/api/webhooks")) return `<span class="chip chip-discord">Discord</span>`;
  return `<span class="chip">Webhook</span>`;
}

function conditionLabel(condition: string | undefined): string {
  if (condition === "new_issue") return "New issue created";
  if (condition === "regression") return "Issue regressed";
  if (condition === "event_count_exceeds") return "Event count exceeds";
  if (condition === "message_contains") return "Message contains";
  return condition || "n/a";
}

function renderAlertList(alerts: ApiAlert[]): string {
  if (!alerts.length) {
    return `
      <div class="empty">
        <strong>No alert rules yet.</strong>
        <p>Use the form below to create the first rule.</p>
      </div>
    `;
  }

  return `
    <div class="route-list">
      ${alerts
        .map((alert) => {
          const id = alert.id ?? "";
          const title = alert.name || "Untitled alert";
          const condition = alert.condition ?? "";
          const param = alert.param ?? "";
          const threshold = alert.threshold ?? 0;
          const webhookUrl = alert.webhook_url ?? "";
          const emailTo = alert.email_to ?? "";
          const cooldown = alert.cooldown_minutes ?? 0;
          const enabled = Boolean(alert.enabled);
          const lastFiredAt = formatTime(alert.last_fired_at) || "never";
          const projectSlug = alert.project_slug ? String(alert.project_slug) : "";
          const conditionDetail = condition === "event_count_exceeds" && threshold ? ` > ${threshold}` : condition === "message_contains" && param ? ` "${param}"` : "";
          return `
            <article class="route-item">
              <div class="route-item-head">
                <strong>${escapeHtml(title)}</strong>
                <span class="chip ${enabled ? "" : "bad"}">${escapeHtml(enabled ? "enabled" : "disabled")}</span>
                ${projectSlug ? `<span class="chip" style="font-size:0.65rem;opacity:0.7">${escapeHtml(projectSlug)}</span>` : ""}
                ${webhookBadge(webhookUrl)}
                ${emailTo ? `<span class="chip">email</span>` : ""}
              </div>
              <div class="route-item-meta">
                <span>${escapeHtml(conditionLabel(condition))}${escapeHtml(conditionDetail)}</span>
                ${cooldown ? `<span>cooldown ${escapeHtml(String(cooldown))}m</span>` : ""}
                <span>last fired: ${escapeHtml(lastFiredAt)}</span>
              </div>
              ${webhookUrl ? `<div class="route-item-meta"><span class="muted url-truncate">${escapeHtml(webhookUrl)}</span></div>` : ""}
              ${emailTo ? `<div class="route-item-meta"><span class="muted">${escapeHtml(emailTo)}</span></div>` : ""}
              <div class="link-row">
                <button class="btn-danger btn-sm" data-action="delete-alert" data-id="${escapeAttr(id)}">Delete</button>
              </div>
            </article>
          `;
        })
        .join("")}
    </div>
  `;
}
