import { escapeAttr, escapeHtml, errorMessage } from "../format.js";
import type { AnalyticsBucket, AnalyticsOverview, AnalyticsPage, AnalyticsReferrer, AnalyticsSegmentBucket, DropoutStat, FlowEntry, PageFlowResult, ScrollDepthResult } from "../types.js";

export function renderAnalyticsViewMarkup(
  overview: AnalyticsOverview | null,
  pages: AnalyticsPage[],
  timeline: AnalyticsBucket[],
  referrers: AnalyticsReferrer[],
  segments: AnalyticsSegmentBucket[],
  rangeDays: number,
  segmentDim: string,
  error: unknown = null,
): string {
  return `
    <div class="view-head">
      <h2>Web analytics</h2>
      <div class="view-actions">
        <div class="analytics-range-bar" role="group" aria-label="Date range">
          <button class="tab${rangeDays === 7 ? " active" : ""}" data-analytics-range="7">7d</button>
          <button class="tab${rangeDays === 30 ? " active" : ""}" data-analytics-range="30">30d</button>
          <button class="tab${rangeDays === 90 ? " active" : ""}" data-analytics-range="90">90d</button>
        </div>
      </div>
    </div>
    ${error ? `<div class="error">Analytics unavailable. ${escapeHtml(errorMessage(error))}</div>` : ""}
    ${renderAnalyticsOverviewCards(overview)}
    ${renderAnalyticsTimeline(timeline)}
    <div class="analytics-tables">
      ${renderAnalyticsPagesTable(pages)}
      ${renderAnalyticsReferrersTable(referrers)}
    </div>
    ${renderAnalyticsSegmentSection(segments, segmentDim)}
  `;
}

function renderAnalyticsOverviewCards(overview: AnalyticsOverview | null): string {
  const pv = overview?.pageviews ?? 0;
  const sv = overview?.sessions ?? 0;
  const pg = overview?.pages ?? 0;
  return `
    <div class="analytics-cards">
      <div class="analytics-card">
        <span class="analytics-card-label">Pageviews</span>
        <strong class="analytics-card-value">${escapeHtml(String(pv))}</strong>
      </div>
      <div class="analytics-card">
        <span class="analytics-card-label">Sessions</span>
        <strong class="analytics-card-value">${escapeHtml(String(sv))}</strong>
      </div>
      <div class="analytics-card">
        <span class="analytics-card-label">Pages</span>
        <strong class="analytics-card-value">${escapeHtml(String(pg))}</strong>
      </div>
    </div>
  `;
}

function renderAnalyticsTimeline(buckets: AnalyticsBucket[]): string {
  if (!buckets.length) {
    return `<div class="section"><div class="empty">No timeline data available.</div></div>`;
  }

  const maxPv = buckets.reduce((m, b) => Math.max(m, b.pageviews), 1);
  const w = 800;
  const h = 120;
  const padTop = 8;
  const padBottom = 8;
  const innerH = h - padTop - padBottom;
  const step = w / Math.max(buckets.length - 1, 1);

  const points = buckets.map((b, i) => {
    const x = Math.round(i * step);
    const y = Math.round(padTop + innerH - (b.pageviews / maxPv) * innerH);
    return `${x},${y}`;
  }).join(" ");

  // Vertical grid lines at each bucket (only if few buckets, else every ~7)
  const gridInterval = buckets.length > 14 ? 7 : 1;
  const gridLines = buckets
    .filter((_, i) => i % gridInterval === 0)
    .map((_, idx) => {
      const i = idx * gridInterval;
      const x = Math.round(i * step);
      return `<line x1="${x}" y1="${padTop}" x2="${x}" y2="${h - padBottom}" stroke="var(--line,#21262d)" stroke-width="1"/>`;
    })
    .join("");

  // Date labels — first and last
  const firstLabel = buckets[0]?.date ?? "";
  const lastLabel = buckets[buckets.length - 1]?.date ?? "";
  const _lastX = Math.round((buckets.length - 1) * step);

  return `
    <div class="section analytics-timeline-section">
      <h3>Pageviews over time</h3>
      <div class="analytics-chart" style="position:relative">
        <svg viewBox="0 0 ${w} ${h}" width="100%" height="${h}" aria-label="Pageviews timeline" role="img" style="display:block;overflow:visible">
          ${gridLines}
          <polyline points="${escapeAttr(points)}" fill="none" stroke="#d4a054" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>
        </svg>
        <div style="display:flex;justify-content:space-between;font-size:11px;color:var(--muted,#8b949e);margin-top:2px">
          <span>${escapeHtml(firstLabel)}</span>
          <span>${escapeHtml(lastLabel)}</span>
        </div>
      </div>
    </div>
  `;
}

function renderAnalyticsPagesTable(pages: AnalyticsPage[]): string {
  if (!pages.length) {
    return `
      <div class="section" style="flex:1;min-width:0">
        <h3>Top pages</h3>
        <div class="empty">
          <p>No page data yet.</p>
          <p class="muted">Install the BugBarn snippet on your site to track pageviews.</p>
        </div>
      </div>
    `;
  }
  const rows = pages.map((p) => `
    <tr>
      <td class="url-truncate">${escapeHtml(p.pathname)}</td>
      <td>${escapeHtml(String(p.pageviews))}</td>
      <td>${escapeHtml(String(p.sessions))}</td>
    </tr>
  `).join("");
  return `
    <div class="section" style="flex:1;min-width:0">
      <h3>Top pages</h3>
      <table class="data-table">
        <thead><tr><th>Path</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>
  `;
}

function renderAnalyticsReferrersTable(referrers: AnalyticsReferrer[]): string {
  if (!referrers.length) {
    return `
      <div class="section" style="flex:1;min-width:0">
        <h3>Referrers</h3>
        <div class="empty"><p>No referrer data yet.</p></div>
      </div>
    `;
  }
  const rows = referrers.map((r) => `
    <tr>
      <td class="url-truncate">${escapeHtml(r.host || "(direct)")}</td>
      <td>${escapeHtml(String(r.pageviews))}</td>
      <td>${escapeHtml(String(r.sessions))}</td>
    </tr>
  `).join("");
  return `
    <div class="section" style="flex:1;min-width:0">
      <h3>Referrers</h3>
      <table class="data-table">
        <thead><tr><th>Host</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>
  `;
}

function renderAnalyticsSegmentSection(segments: AnalyticsSegmentBucket[], dim: string): string {
  const dimOptions = [
    { value: "", label: "-- none --" },
    { value: "referrer_host", label: "Referrer host" },
    { value: "screen_width", label: "Screen width" },
  ];
  const selectHtml = `
    <select id="analytics-segment-dim" aria-label="Breakdown dimension">
      ${dimOptions.map((o) => `<option value="${escapeAttr(o.value)}"${o.value === dim ? " selected" : ""}>${escapeHtml(o.label)}</option>`).join("")}
    </select>
  `;
  if (!dim) {
    return `
      <div class="section">
        <h3>Breakdown</h3>
        ${selectHtml}
      </div>
    `;
  }
  let tableHtml = `<div class="empty"><p>No segment data.</p></div>`;
  if (segments.length) {
    const rows = segments.map((s) => `
      <tr>
        <td>${escapeHtml(s.value || "(unknown)")}</td>
        <td>${escapeHtml(String(s.pageviews))}</td>
        <td>${escapeHtml(String(s.sessions))}</td>
      </tr>
    `).join("");
    tableHtml = `
      <table class="data-table">
        <thead><tr><th>${escapeHtml(dim)}</th><th>Views</th><th>Sessions</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    `;
  }
  return `
    <div class="section">
      <h3>Breakdown</h3>
      ${selectHtml}
      ${tableHtml}
    </div>
  `;
}

export function renderAnalyticsPagesWithDropout(pages: AnalyticsPage[], dropoutMap: Map<string, DropoutStat>): string {
  if (!pages.length) return `<div class="empty"><p>No page data yet.</p></div>`;
  const rows = pages.map((p) => {
    const d = dropoutMap.get(p.pathname);
    const bouncePct = d ? (d.bounceRate * 100).toFixed(1) + "%" : "—";
    return `<tr class="analytics-page-row" data-pathname="${escapeAttr(p.pathname)}" style="cursor:pointer">
      <td>${escapeHtml(p.pathname)}</td>
      <td style="text-align:right">${escapeHtml(String(p.pageviews))}</td>
      <td style="text-align:right">${escapeHtml(String(p.sessions))}</td>
      <td style="text-align:right">${escapeHtml(bouncePct)}</td>
    </tr>`;
  }).join("");
  return `<table class="data-table" style="width:100%">
    <thead><tr><th>Page</th><th style="text-align:right">Views</th><th style="text-align:right">Sessions</th><th style="text-align:right">Bounce %</th></tr></thead>
    <tbody>${rows}</tbody>
  </table>`;
}

export function renderPageDetail(flow: PageFlowResult, scroll: ScrollDepthResult): string {
  const scrollBars = scroll.buckets.map((b) => {
    const w = Math.max(2, Math.round(b.pct));
    return `<div style="display:flex;align-items:center;gap:8px;margin:3px 0">
      <span style="min-width:60px;font-size:12px">${escapeHtml(b.label)}</span>
      <div style="flex:1;background:var(--line,#21262d);border-radius:3px;height:12px;overflow:hidden">
        <div style="width:${escapeAttr(String(w))}%;background:var(--accent,#d4a054);height:100%"></div>
      </div>
      <span style="min-width:36px;font-size:12px;text-align:right">${escapeHtml(b.pct.toFixed(1))}%</span>
    </div>`;
  }).join("");
  const flowTable = (entries: FlowEntry[], empty: string) =>
    entries.length ? `<table style="width:100%;border-collapse:collapse;font-size:12px">
      <thead><tr><th style="text-align:left">Page</th><th style="text-align:right">Count</th><th style="text-align:right">%</th></tr></thead>
      <tbody>${entries.map((e) => `<tr>
        <td>${escapeHtml(e.pathname)}</td>
        <td style="text-align:right">${escapeHtml(String(e.count))}</td>
        <td style="text-align:right">${escapeHtml(e.pct.toFixed(1))}%</td>
      </tr>`).join("")}</tbody>
    </table>` : `<p class="muted" style="font-size:12px">${escapeHtml(empty)}</p>`;
  return `<div style="padding:12px 0">
    <h4 style="margin:0 0 10px">${escapeHtml(flow.pathname)}</h4>
    <div class="section"><h3>Scroll depth</h3>${scrollBars || `<p class="muted">No data yet.</p>`}</div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:12px">
      <div class="section"><h3>Came from</h3>${flowTable(flow.cameFrom, "No upstream pages.")}</div>
      <div class="section"><h3>Went to</h3>${flowTable(flow.wentTo, "No downstream pages.")}</div>
    </div>
  </div>`;
}
