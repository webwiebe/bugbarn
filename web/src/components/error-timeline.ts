// Stacked bar chart of error events per project, shown above the issues list.
import { escapeAttr, escapeHtml } from "../format.js";
import { stackTimeline, type Bar, type Series, type TimelineData } from "../timeline-stack.js";

export const TIMELINE_RANGES = ["24h", "7d", "30d"] as const;
export type TimelineRange = (typeof TIMELINE_RANGES)[number];

const RANGE_LABELS: Record<TimelineRange, string> = { "24h": "24 hours", "7d": "7 days", "30d": "30 days" };

function barTitle(bar: Bar, range: string): string {
  const when = new Date(bar.start);
  const label = range === "30d" ? when.toLocaleDateString() : when.toLocaleString([], { dateStyle: "short", timeStyle: "short" });
  const lines = bar.segments.map((s) => `${s.name}: ${s.count}`);
  return [`${label}, ${bar.total} errors`, ...lines].join("\n");
}

function renderBar(bar: Bar, series: Series[], max: number, range: string): string {
  const height = max > 0 ? (bar.total / max) * 100 : 0;
  const colors = new Map(series.map((s) => [s.key, s.color]));
  const segments = bar.segments
    .map((seg) => `<span class="error-timeline-seg" style="flex:${seg.count};background:${colors.get(seg.key) ?? "#888"}"></span>`)
    .join("");
  return `<span class="error-timeline-bar" style="height:${height.toFixed(1)}%" title="${escapeAttr(barTitle(bar, range))}">${segments}</span>`;
}

function renderLegendItem(s: Series): string {
  const dot = `<span class="error-timeline-dot" style="background:${s.color}"></span>`;
  const text = `${dot}<span class="error-timeline-name">${escapeHtml(s.name)}</span><span class="error-timeline-total">${s.total}</span>`;
  if (s.isOther) return `<li class="error-timeline-legend-item">${text}</li>`;
  return `<li class="error-timeline-legend-item"><button type="button" class="error-timeline-legend-btn" data-timeline-project="${escapeAttr(s.key)}" title="Show only ${escapeAttr(s.name)}">${text}</button></li>`;
}

function renderRangeButtons(range: TimelineRange): string {
  return TIMELINE_RANGES.map((r) => {
    const pressed = r === range;
    return `<button type="button" class="error-timeline-range${pressed ? " active" : ""}" data-timeline-range="${r}" aria-pressed="${pressed}">${RANGE_LABELS[r]}</button>`;
  }).join("");
}

function renderBody(data: TimelineData | null, failed: boolean, range: TimelineRange): string {
  if (failed) return `<p class="error-timeline-empty">The error timeline is unavailable.</p>`;
  if (!data) return `<p class="error-timeline-empty">Loading…</p>`;
  const stacked = stackTimeline(data);
  if (stacked.series.length === 0) {
    return `<p class="error-timeline-empty">No errors in the last ${RANGE_LABELS[range]}.</p>`;
  }
  const bars = stacked.bars.map((b) => renderBar(b, stacked.series, stacked.max, data.range)).join("");
  const legend = stacked.series.map(renderLegendItem).join("");
  return `<div class="error-timeline-bars" style="grid-template-columns:repeat(${stacked.bars.length},minmax(2px,1fr))">${bars}</div><ul class="error-timeline-legend">${legend}</ul>`;
}

export function renderErrorTimeline(data: TimelineData | null, failed: boolean, range: TimelineRange): string {
  return `<section class="error-timeline" id="error-timeline" aria-label="Errors per project">
    <div class="error-timeline-head"><h3>Errors per project</h3><div class="error-timeline-ranges">${renderRangeButtons(range)}</div></div>
    ${renderBody(data, failed, range)}
  </section>`;
}

/** Wires the range buttons and legend entries of a rendered timeline. */
export function bindErrorTimeline(root: ParentNode, onRange: (range: TimelineRange) => void): void {
  root.querySelectorAll<HTMLButtonElement>("[data-timeline-range]").forEach((btn) => {
    btn.addEventListener("click", () => onRange(btn.dataset["timelineRange"] as TimelineRange));
  });
  root.querySelectorAll<HTMLButtonElement>("[data-timeline-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.dispatchEvent(new CustomEvent("bugbarn:select-project", { detail: btn.dataset["timelineProject"] }));
    });
  });
}
