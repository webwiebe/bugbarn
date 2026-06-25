import { escapeAttr, escapeHtml, formatTime } from "../format.js";
import type { ApiLogEntry } from "../types.js";
import { toTimestampMs } from "./shared.js";

function renderLogData(data: Record<string, unknown>): string {
  const keys = Object.keys(data).slice(0, 6);
  return keys
    .map((k) => {
      const v = data[k] ?? "";
      const raw = typeof v === "object" && v !== null ? JSON.stringify(v) : String(v);
      const val = raw.length > 40 ? `${raw.slice(0, 37)}…` : raw;
      return `<span class="log-pill">${escapeHtml(k)}: <strong>${escapeHtml(val)}</strong></span>`;
    })
    .join("");
}

const logTimelineBucketCount = 40;

function renderLogTimeline(logs: ApiLogEntry[]): string {
  if (!logs.length) return "";

  const timestamps = logs
    .map((l) => toTimestampMs(l.received_at))
    .filter((ts) => ts > 0)
    .sort((a, b) => a - b);

  if (!timestamps.length) return "";

  const minTs = timestamps[0];
  const maxTs = timestamps[timestamps.length - 1];
  const span = Math.max(maxTs - minTs, 60 * 1000);
  const bucketSize = span / logTimelineBucketCount;
  const buckets = Array.from({ length: logTimelineBucketCount }, () => 0);

  for (const ts of timestamps) {
    const bucket = Math.min(logTimelineBucketCount - 1, Math.max(0, Math.floor((ts - minTs) / bucketSize)));
    buckets[bucket] += 1;
  }
  const maxBucket = Math.max(...buckets, 1);

  return `
    <div class="log-timeline">
      <div class="log-timeline-bars">
        ${buckets
          .map((count) => {
            const height = count ? Math.max(8, Math.round((count / maxBucket) * 100)) : 2;
            return `<span class="log-timeline-bar${count ? " active" : ""}" style="height:${escapeAttr(String(height))}%" title="${escapeAttr(`${count} logs`)}"></span>`;
          })
          .join("")}
      </div>
      <div class="log-timeline-axis">
        <span>${escapeHtml(formatTime(minTs) || "Start")}</span>
        <span>${escapeHtml(formatTime(maxTs) || "Now")}</span>
      </div>
    </div>
  `;
}

export function renderLogRow(entry: ApiLogEntry): string {
  const hasData = entry.data && Object.keys(entry.data).length > 0;
  const dataPills = hasData ? `<div class="log-pills">${renderLogData(entry.data as Record<string, unknown>)}</div>` : "";
  const dataExpanded = hasData
    ? `<div class="log-data-expanded"><pre>${escapeHtml(JSON.stringify(entry.data, null, 2))}</pre></div>`
    : "";
  const projectBadge = entry.project_slug ? `<span class="log-project-badge">${escapeHtml(entry.project_slug)}</span>` : "";
  const levelNum = entry.level_num ?? 0;
  return `
    <div class="log-row log-row-${escapeAttr(entry.level)}" data-log-id="${escapeAttr(String(entry.id))}">
      <div class="log-header">
        <span class="log-level log-level-${escapeAttr(entry.level)}"><span class="log-level-dot"></span>${escapeHtml(entry.level.toUpperCase())}${levelNum ? ` (${escapeHtml(String(levelNum))})` : ""}</span>
        ${projectBadge}
        <span class="log-time">${escapeHtml(formatTime(entry.received_at))}</span>
      </div>
      <div class="log-body">
        <span class="log-msg">${escapeHtml(entry.message)}</span>
        ${dataPills}
      </div>
      ${dataExpanded}
    </div>
  `;
}

export function renderLogsViewMarkup(logs: ApiLogEntry[], level: string, search: string): string {
  const count = logs.length;
  const levelOptions = [
    { value: "", label: "All levels" },
    { value: "trace", label: "Trace" },
    { value: "debug", label: "Debug" },
    { value: "info", label: "Info" },
    { value: "warn", label: "Warn" },
    { value: "error", label: "Error" },
    { value: "fatal", label: "Fatal" },
  ];

  const listContent = count
    ? logs.map(renderLogRow).join("")
    : `<div class="empty">No log entries yet. Connect a project to start streaming logs.</div>`;

  const timeline = renderLogTimeline(logs);

  return `
    <div class="view-head">
      <h2>Log stream</h2>
      <span class="chip">${escapeHtml(String(count))}</span>
    </div>
    ${timeline}
    <div class="log-toolbar">
      <select id="log-level-filter" aria-label="Filter by level">
        ${levelOptions.map((opt) => `<option value="${escapeAttr(opt.value)}"${opt.value === level ? " selected" : ""}>${escapeHtml(opt.label)}</option>`).join("")}
      </select>
      <input id="log-search" type="search" placeholder="Filter by message…" value="${escapeAttr(search)}" />
      <button id="log-clear" type="button">Clear</button>
      <span class="log-live-indicator" id="log-live-dot"></span>
    </div>
    <div id="log-list" class="log-list">
      ${listContent}
    </div>
  `;
}
