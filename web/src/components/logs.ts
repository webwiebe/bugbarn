import { escapeAttr, escapeHtml, formatTime } from "../format.js";
import type { LogFilters } from "./log-filters.js";
import type { ApiLogEntry } from "../types.js";
import { renderAttrChips, renderLogFacets, renderLogHistogram, renderRangeSelect, type LogExplorerPanels } from "./log-explorer.js";

function attrText(v: unknown): string {
  return typeof v === "object" && v !== null ? JSON.stringify(v) : String(v ?? "");
}

// A pill is a button-like span: clicking it adds the attribute as a filter.
function renderLogData(data: Record<string, unknown>): string {
  const keys = Object.keys(data).slice(0, 6);
  return keys
    .map((k) => {
      const raw = attrText(data[k]);
      const val = raw.length > 40 ? `${raw.slice(0, 37)}…` : raw;
      return `<span class="log-pill" role="button" tabindex="0" title="Filter on ${escapeAttr(k)}" data-attr-k="${escapeAttr(k)}" data-attr-v="${escapeAttr(raw)}">${escapeHtml(k)}: <strong>${escapeHtml(val)}</strong></span>`;
    })
    .join("");
}

function traceIdOf(data: Record<string, unknown> | undefined): string {
  const id = data?.["trace_id"] ?? data?.["traceId"];
  return typeof id === "string" ? id : "";
}

function renderTraceLink(traceId: string, traceBase: string): string {
  if (!traceId) return "";
  if (!/^https?:\/\//.test(traceBase)) {
    return `<div class="log-trace">trace_id: <code>${escapeHtml(traceId)}</code></div>`;
  }
  const href = `${traceBase}/traces/${encodeURIComponent(traceId)}`;
  return `<div class="log-trace">trace_id: <a href="${escapeAttr(href)}" target="_blank" rel="noopener noreferrer">${escapeHtml(traceId)}</a></div>`;
}

export function renderLogRow(entry: ApiLogEntry, traceBase = ""): string {
  const hasData = entry.data && Object.keys(entry.data).length > 0;
  const dataPills = hasData ? `<div class="log-pills">${renderLogData(entry.data as Record<string, unknown>)}</div>` : "";
  const dataExpanded = hasData
    ? `<div class="log-data-expanded">${renderTraceLink(traceIdOf(entry.data), traceBase)}<pre>${escapeHtml(JSON.stringify(entry.data, null, 2))}</pre></div>`
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

export function renderLogsViewMarkup(
  logs: ApiLogEntry[],
  filters: LogFilters,
  panels: LogExplorerPanels,
  traceBase: string,
): string {
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
    ? logs.map((entry) => renderLogRow(entry, traceBase)).join("")
    : `<div class="empty">No log entries match these filters.</div>`;

  return `
    <div class="view-head">
      <h2>Log explorer</h2>
      <span class="chip">${escapeHtml(String(count))}</span>
    </div>
    ${renderLogHistogram(panels.histogram)}
    <div class="log-toolbar">
      ${renderRangeSelect(filters)}
      <select id="log-level-filter" aria-label="Filter by level">
        ${levelOptions.map((opt) => `<option value="${escapeAttr(opt.value)}"${opt.value === filters.level ? " selected" : ""}>${escapeHtml(opt.label)}</option>`).join("")}
      </select>
      <input id="log-search" type="search" placeholder="Filter by message…" value="${escapeAttr(filters.q)}" />
      <button id="log-clear" type="button">Clear</button>
      <span class="log-live-indicator" id="log-live-dot"></span>
    </div>
    ${renderAttrChips(filters)}
    ${renderLogFacets(filters, panels)}
    <div id="log-list" class="log-list">
      ${listContent}
    </div>
  `;
}
