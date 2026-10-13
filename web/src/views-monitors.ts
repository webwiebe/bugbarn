// Monitors: scheduled jobs and workers that report in with check-ins. Read-only;
// a monitor is created by its first check-in. Ones that need attention come first.
import { errorMessage, escapeHtml } from "./format.js";
import { elements, setActiveView, state } from "./core.js";
import { telemetryGet } from "./telemetry-api.js";

interface Monitor {
  slug: string;
  project: string;
  state: "ok" | "missed" | "error";
  lastStatus: string;
  intervalSeconds: number;
  graceSeconds: number;
  dueBy: string;
  stateSince: string;
  lastCheckInAt?: string;
}

const mv = { monitors: [] as Monitor[], error: "", loaded: false };

export async function loadMonitors(): Promise<void> {
  try {
    const res = await telemetryGet<{ monitors: Monitor[] }>("/api/v1/monitors");
    mv.monitors = res.monitors ?? [];
    mv.error = "";
  } catch (error) {
    mv.monitors = [];
    mv.error = errorMessage(error);
  }
  mv.loaded = true;
  if (state.currentRoute === "monitors") renderMonitorsView();
}

export function monitorsStatus(): string {
  const bad = mv.monitors.filter(m => m.state !== "ok").length;
  const total = `${mv.monitors.length} monitor${mv.monitors.length === 1 ? "" : "s"}`;
  return bad ? `${total}, ${bad} need${bad === 1 ? "s" : ""} attention.` : `${total}.`;
}

/** formatSeconds writes a duration the way a schedule is spoken: 1h, 10m, 90s. */
export function formatSeconds(total: number): string {
  if (total <= 0) return "0s";
  const units: [string, number][] = [["d", 86400], ["h", 3600], ["m", 60], ["s", 1]];
  const parts: string[] = [];
  let rest = total;
  for (const [name, size] of units) {
    const n = Math.floor(rest / size);
    if (n > 0) parts.push(`${n}${name}`);
    rest -= n * size;
  }
  return parts.slice(0, 2).join(" ");
}

/** monitorAge says how long ago a timestamp was, or "never" when there is none. */
export function monitorAge(iso: string | undefined, now = Date.now()): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (!Number.isFinite(t)) return "never";
  const secs = Math.max(0, Math.round((now - t) / 1000));
  return `${formatSeconds(secs)} ago`;
}

function stateChip(m: Monitor): string {
  if (m.state === "missed") return `<span class="chip warn">missed</span>`;
  if (m.state === "error") return `<span class="chip warn">failing</span>`;
  return `<span class="chip">ok</span>`;
}

function renderRow(m: Monitor): string {
  const since = m.state === "ok" ? "" : `<div class="muted">since ${escapeHtml(monitorAge(m.stateSince))}</div>`;
  return `
    <tr class="${m.state === "ok" ? "" : "attention"}">
      <td><strong>${escapeHtml(m.slug)}</strong>${m.project ? `<div class="muted sec-mono">${escapeHtml(m.project)}</div>` : ""}</td>
      <td>${stateChip(m)}${since}</td>
      <td>${escapeHtml(monitorAge(m.lastCheckInAt))}</td>
      <td>every ${escapeHtml(formatSeconds(m.intervalSeconds))}, grace ${escapeHtml(formatSeconds(m.graceSeconds))}</td>
    </tr>`;
}

function renderList(): string {
  if (mv.error) return `<div class="callout callout-error"><span>${escapeHtml(mv.error)}</span></div>`;
  if (!mv.loaded) return `<div class="empty">Loading monitors…</div>`;
  if (!mv.monitors.length) {
    return `<div class="empty">No monitors yet. A job creates its monitor with its first check-in; see the API docs under POST /api/v1/monitors/{slug}/check-ins.</div>`;
  }
  return `
    <div class="table-scroll">
      <table class="volume-table">
        <thead><tr><th>Monitor</th><th>State</th><th>Last check-in</th><th>Schedule</th></tr></thead>
        <tbody>${mv.monitors.map(renderRow).join("")}</tbody>
      </table>
    </div>`;
}

export function renderMonitorsView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Monitors";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = `
    <div class="view-head"><h2>Monitors</h2></div>
    <p class="muted">Scheduled jobs and workers that report in. A job that stays silent past its interval plus grace opens an issue, and its next ok check-in resolves it.</p>
    <div class="section">${renderList()}</div>`;
}
