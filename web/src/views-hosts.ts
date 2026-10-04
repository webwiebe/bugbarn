// Host health: the hosts that ship metrics, when each last reported, and
// per-host charts of CPU, load, memory, filesystems and network.
import { renderLineChart } from "./components/chart.js";
import { escapeAttr, escapeHtml, errorMessage, formatAge, formatTime } from "./format.js";
import { elements, setActiveView, state } from "./core.js";
import { isStale, telemetryGet } from "./telemetry-api.js";

interface Host {
  host: string;
  cores: number;
  lastSeen: string;
}

interface Point {
  ts: string;
  value: number;
  min: number;
  max: number;
}

interface SeriesResponse {
  metric: string;
  resolution: string;
  points: Point[];
}

const hostRanges: [number, string][] = [
  [3600000, "1h"], [21600000, "6h"], [86400000, "24h"], [604800000, "7d"], [2592000000, "30d"], [7776000000, "90d"],
];

const hv = {
  hosts: [] as Host[],
  hostsError: "",
  selected: null as string | null,
  rangeMs: 86400000,
  series: [] as SeriesResponse[],
  seriesError: "",
  loading: false,
};

/** setSelectedHost is called by the router: null shows the host list. */
export function setSelectedHost(host: string | null): void {
  if (host !== hv.selected) hv.series = [];
  hv.selected = host;
}

export function hostsStatus(): string {
  if (hv.selected) return `${hv.series.length} chart${hv.series.length === 1 ? "" : "s"} for ${hv.selected}.`;
  return `${hv.hosts.length} host${hv.hosts.length === 1 ? "" : "s"} reporting.`;
}

export async function loadHosts(): Promise<void> {
  try {
    const res = await telemetryGet<{ hosts: Host[] }>("/api/v1/telemetry/hosts");
    hv.hosts = res.hosts ?? [];
    hv.hostsError = "";
  } catch (error) {
    hv.hosts = [];
    hv.hostsError = errorMessage(error);
  }
  if (hv.selected) await loadHostCharts();
  renderIfActive();
}

/** chartMetrics picks the metrics worth a chart, in display order. */
export function chartMetrics(names: string[]): string[] {
  const fixed = ["cpu.util", "load1", "mem.avail_pct"].filter(n => names.includes(n));
  const fs = names.filter(n => n.startsWith("fs.") && n.endsWith(".used_pct"));
  const net = names.filter(n => n.startsWith("net.") && /\.(rx|tx)_bytes_per_s$/.test(n));
  return [...fixed, ...fs, ...net];
}

async function loadHostCharts(): Promise<void> {
  const host = hv.selected;
  if (!host) return;
  hv.loading = true;
  renderIfActive();
  try {
    const base = `/api/v1/telemetry/hosts/${encodeURIComponent(host)}`;
    const list = await telemetryGet<{ metrics: string[] }>(`${base}/metrics`);
    const to = Date.now();
    const from = to - hv.rangeMs;
    const series = await Promise.all(chartMetrics(list.metrics ?? []).map(m =>
      telemetryGet<SeriesResponse>(`${base}/series?metric=${encodeURIComponent(m)}&from=${from}&to=${to}`)));
    if (hv.selected === host) {
      hv.series = series;
      hv.seriesError = "";
    }
  } catch (error) {
    hv.series = [];
    hv.seriesError = errorMessage(error);
  } finally {
    hv.loading = false;
  }
}

function renderIfActive(): void {
  if (state.currentRoute === "hosts") renderHostsView();
}

function renderHostRow(h: Host): string {
  const stale = isStale(h.lastSeen);
  const badge = stale
    ? `<span class="chip bad">stale</span>`
    : `<span class="chip">live</span>`;
  return `
    <tr class="host-row" data-host="${escapeAttr(h.host)}" tabindex="0">
      <td><a href="#/hosts/${escapeAttr(encodeURIComponent(h.host))}">${escapeHtml(h.host)}</a></td>
      <td class="volume-num">${h.cores || "?"}</td>
      <td title="${escapeAttr(formatTime(h.lastSeen))}">${escapeHtml(formatAge(h.lastSeen))} ago</td>
      <td>${badge}</td>
    </tr>`;
}

function renderHostList(): string {
  if (hv.hostsError) return `<div class="callout callout-error"><span>${escapeHtml(hv.hostsError)}</span></div>`;
  if (!hv.hosts.length) return `<div class="empty">No host has reported metrics yet.</div>`;
  return `
    <div class="table-scroll">
      <table class="volume-table">
        <thead><tr><th>Host</th><th class="volume-num">Cores</th><th>Last seen</th><th></th></tr></thead>
        <tbody>${hv.hosts.map(renderHostRow).join("")}</tbody>
      </table>
    </div>
    <p class="muted">A host is stale after 5 minutes without metrics.</p>`;
}

function formatValue(metric: string, v: number): string {
  if (metric.endsWith("_pct") || metric === "cpu.util") return `${v.toFixed(1)}%`;
  if (metric.endsWith("_bytes_per_s")) {
    const units = ["B/s", "KB/s", "MB/s", "GB/s"];
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(1)} ${units[i]}`;
  }
  return v.toFixed(2);
}

function metricTitle(metric: string, cores: number): string {
  if (metric === "cpu.util") return "CPU utilization";
  if (metric === "load1") return cores ? `Load (1m), ${cores} cores` : "Load (1m)";
  if (metric === "mem.avail_pct") return "Memory available";
  return metric;
}

function renderChartCard(s: SeriesResponse, cores: number): string {
  const values = s.points.map(p => p.value);
  const last = values[values.length - 1];
  const percent = s.metric.endsWith("_pct") || s.metric === "cpu.util";
  const chart = renderLineChart(values, {
    ariaLabel: `${s.metric} over time`,
    xs: s.points.map(p => Date.parse(p.ts)),
    maxValue: percent ? 100 : undefined,
    startLabel: formatTime(s.points[0]?.ts),
    endLabel: formatTime(s.points[s.points.length - 1]?.ts),
  });
  const peak = values.length ? Math.max(...values) : 0;
  return `
    <div class="section host-chart">
      <div class="section-head">
        <h3>${escapeHtml(metricTitle(s.metric, cores))}</h3>
        <span class="muted">${last === undefined ? "no data" : `now ${escapeHtml(formatValue(s.metric, last))}, peak ${escapeHtml(formatValue(s.metric, peak))}`}
          · ${escapeHtml(s.resolution)}</span>
      </div>
      ${chart || `<div class="empty">No samples in this range.</div>`}
    </div>`;
}

function renderHostDetail(host: string): string {
  const info = hv.hosts.find(h => h.host === host);
  const ranges = hostRanges.map(([ms, label]) =>
    `<button type="button" class="tab${ms === hv.rangeMs ? " active" : ""}" data-host-range="${ms}">${label}</button>`).join("");
  let body: string;
  if (hv.seriesError) body = `<div class="callout callout-error"><span>${escapeHtml(hv.seriesError)}</span></div>`;
  else if (hv.loading && !hv.series.length) body = `<div class="empty">Loading charts…</div>`;
  else if (!hv.series.length) body = `<div class="empty">This host has reported no chartable metrics in the last 7 days.</div>`;
  else body = `<div class="host-charts">${hv.series.map(s => renderChartCard(s, info?.cores ?? 0)).join("")}</div>`;
  const seen = info ? `Last seen ${escapeHtml(formatAge(info.lastSeen))} ago${isStale(info.lastSeen) ? " (stale)" : ""}` : "";
  return `
    <div class="view-head">
      <h2><a href="#/hosts">Hosts</a> / ${escapeHtml(host)}</h2>
      <div class="view-actions"><div class="analytics-range-bar" role="group" aria-label="Time range">${ranges}</div></div>
    </div>
    <p class="muted">${seen}</p>
    ${body}`;
}

export function renderHostsView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Hosts";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = hv.selected
    ? renderHostDetail(hv.selected)
    : `<div class="view-head"><h2>Hosts</h2></div><div class="section">${renderHostList()}</div>`;
  wireHostsView();
}

function wireHostsView(): void {
  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-host-range]").forEach((btn) => {
    btn.addEventListener("click", () => {
      hv.rangeMs = Number(btn.getAttribute("data-host-range"));
      void loadHostCharts().then(renderIfActive);
    });
  });
  elements.overviewView.querySelectorAll<HTMLElement>(".host-row").forEach((row) => {
    row.addEventListener("click", (ev) => {
      if ((ev.target as HTMLElement).closest("a")) return;
      location.hash = `#/hosts/${encodeURIComponent(row.getAttribute("data-host") ?? "")}`;
    });
  });
}
