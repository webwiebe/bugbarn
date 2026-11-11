// Markup of the Logs explorer: range select, attribute chips, the volume chart
// by level and the facet panel. Behaviour lives in views-logs.ts.
import { escapeAttr, escapeHtml, formatTime } from "../format.js";
import { logRanges, splitAttr, type LogFilters } from "./log-filters.js";
import type { ApiLogFacets, ApiLogHistogram } from "../types.js";

export interface LogExplorerPanels {
  histogram: ApiLogHistogram | null;
  facetKeys: ApiLogFacets | null;
  facetValues: ApiLogFacets | null;
}

const chartLevels = ["fatal", "error", "warn", "info", "debug", "trace"];

export function renderRangeSelect(filters: LogFilters): string {
  const custom = filters.from && filters.to;
  const options = logRanges
    .map((r) => `<option value="${escapeAttr(r)}"${!custom && r === filters.range ? " selected" : ""}>Last ${escapeHtml(r)}</option>`)
    .join("");
  return `<select id="log-range" aria-label="Time range">${custom ? `<option value="" selected>Custom range</option>` : ""}${options}</select>`;
}

export function renderAttrChips(filters: LogFilters): string {
  const chips = filters.attrs
    .map((attr) => {
      const { key, value } = splitAttr(attr);
      return `<button type="button" class="log-chip" data-attr-remove="${escapeAttr(attr)}" title="Remove filter">${escapeHtml(key)} = ${escapeHtml(value)} <span aria-hidden="true">×</span></button>`;
    })
    .join("");
  const reset = filters.from && filters.to
    ? `<button type="button" class="log-chip" id="log-range-reset" title="Back to the selected range">${escapeHtml(formatTime(filters.from))} to ${escapeHtml(formatTime(filters.to))} <span aria-hidden="true">×</span></button>`
    : "";
  return reset || chips ? `<div class="log-chips" id="log-chips">${reset}${chips}</div>` : "";
}

function barTotal(counts: Record<string, number>): number {
  return Object.values(counts).reduce((sum, n) => sum + n, 0);
}

export function renderLogHistogram(histogram: ApiLogHistogram | null): string {
  if (!histogram || !histogram.buckets.length) return "";
  const max = Math.max(1, ...histogram.buckets.map((b) => barTotal(b.counts)));
  const bars = histogram.buckets
    .map((bucket) => {
      const total = barTotal(bucket.counts);
      const segments = chartLevels
        .filter((level) => bucket.counts[level])
        .map((level) => `<span class="log-hist-seg log-hist-${escapeAttr(level)}" style="flex-grow:${escapeAttr(String(bucket.counts[level]))}"></span>`)
        .join("");
      const height = total ? Math.max(6, Math.round((total / max) * 100)) : 2;
      const breakdown = chartLevels.filter((l) => bucket.counts[l]).map((l) => `${l} ${bucket.counts[l]}`).join(", ");
      const title = `${formatTime(bucket.start)}: ${total ? breakdown : "no logs"}`;
      return `<button type="button" class="log-hist-bar" data-bucket-start="${escapeAttr(bucket.start)}" data-bucket-seconds="${escapeAttr(String(histogram.bucket_seconds))}" title="${escapeAttr(title)}" aria-label="${escapeAttr(title)}"><span class="log-hist-stack" style="height:${escapeAttr(String(height))}%">${segments}</span></button>`;
    })
    .join("");
  const legend = chartLevels
    .map((l) => `<span class="log-hist-legend"><span class="log-hist-seg log-hist-${escapeAttr(l)}"></span>${escapeHtml(l)}</span>`)
    .join("");
  const note = histogram.truncated
    ? `<div class="log-note">Only the newest log rows are searched; older rows in this range are not counted.</div>`
    : "";
  return `
    <div class="log-timeline" id="log-histogram">
      <div class="log-hist-bars" style="grid-template-columns:repeat(${escapeAttr(String(histogram.buckets.length))},minmax(2px,1fr))">${bars}</div>
      <div class="log-timeline-axis">
        <span>${escapeHtml(formatTime(histogram.from))}</span>
        <span class="log-hist-legends">${legend}</span>
        <span>${escapeHtml(formatTime(histogram.to))}</span>
      </div>
      ${note}
    </div>`;
}

export function renderLogFacets(filters: LogFilters, panels: LogExplorerPanels): string {
  const keys = panels.facetKeys?.items ?? [];
  if (!keys.length) return "";
  const keyButtons = keys
    .map((k) => `<button type="button" class="log-facet-key${k.value === filters.facetKey ? " active" : ""}" data-facet-key="${escapeAttr(k.value)}">${escapeHtml(k.value)} <span class="log-facet-count">${escapeHtml(String(k.count))}</span></button>`)
    .join("");
  let values = "";
  if (filters.facetKey) {
    const items = panels.facetValues?.items ?? [];
    values = items.length
      ? `<div class="log-facet-values">${items
          .map((v) => `<button type="button" class="log-facet-value" data-facet-add-key="${escapeAttr(filters.facetKey)}" data-facet-add-value="${escapeAttr(v.value)}">${escapeHtml(v.value || "(empty)")} <span class="log-facet-count">${escapeHtml(String(v.count))}</span></button>`)
          .join("")}</div>`
      : `<div class="log-note">No values for ${escapeHtml(filters.facetKey)} in this range.</div>`;
  }
  const more = panels.facetKeys?.truncated ? `<span class="log-note">Top attribute keys only.</span>` : "";
  return `<div class="log-facets" id="log-facets"><div class="log-facet-keys"><span class="log-facet-title">Attributes</span>${keyButtons}${more}</div>${values}</div>`;
}
