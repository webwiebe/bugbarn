// Security log search: a time-range search over the normalized security logs
// (Traefik, k8s audit, sshd/journald, Cloudflare) with keyset "load more" and
// a raw JSON expand per row.
import { escapeAttr, escapeHtml, errorMessage, formatTime } from "./format.js";
import { elements, setActiveView, setStatus, state } from "./core.js";
import { telemetryGet } from "./telemetry-api.js";

interface SecurityRow {
  id: number;
  ts: string;
  source: string;
  host: string;
  kind: string;
  src_ip: string;
  user: string;
  action: string;
  status: number;
  method: string;
  path: string;
  message: string;
  raw: string;
}

interface SecurityPage {
  rows: SecurityRow[];
  next_cursor: string | null;
}

const filterFields = ["source", "host", "src_ip", "user", "kind", "status", "q"] as const;
type FilterField = typeof filterFields[number];

const filterLabels: Record<FilterField, string> = {
  source: "Source", host: "Host", src_ip: "Source IP", user: "User", kind: "Kind", status: "Status", q: "Text",
};

const rangeOptions: [string, string][] = [
  ["3600000", "Last hour"], ["21600000", "Last 6 hours"], ["86400000", "Last 24 hours"],
  ["604800000", "Last 7 days"], ["2592000000", "Last 30 days"], ["custom", "Custom"],
];

const sec = {
  range: "86400000",
  customFrom: "",
  customTo: "",
  filters: {} as Partial<Record<FilterField, string>>,
  // The window is fixed when a search starts, so "load more" pages through
  // the same rows even while new ones arrive.
  windowFrom: 0,
  windowTo: 0,
  rows: [] as SecurityRow[],
  next: null as string | null,
  error: "",
  loading: false,
};

function currentWindow(): [number, number] {
  if (sec.range !== "custom") {
    const to = Date.now();
    return [to - Number(sec.range), to];
  }
  const to = sec.customTo ? Date.parse(sec.customTo) : Date.now();
  const from = sec.customFrom ? Date.parse(sec.customFrom) : to - 86400000;
  return [from, to];
}

function searchPath(cursor: string | null): string {
  const params = new URLSearchParams();
  params.set("from", String(sec.windowFrom));
  params.set("to", String(sec.windowTo));
  for (const f of filterFields) {
    const v = sec.filters[f]?.trim();
    if (v) params.set(f, v);
  }
  params.set("limit", "100");
  if (cursor) params.set("cursor", cursor);
  return `/api/v1/telemetry/security?${params.toString()}`;
}

/** loadSecurity runs a fresh search with the current form values. */
export async function loadSecurity(): Promise<void> {
  [sec.windowFrom, sec.windowTo] = currentWindow();
  if (!Number.isFinite(sec.windowFrom) || !Number.isFinite(sec.windowTo)) {
    sec.error = "Enter a valid custom time range.";
    renderIfActive();
    return;
  }
  await fetchPage(null);
}

async function fetchPage(cursor: string | null): Promise<void> {
  sec.loading = true;
  sec.error = "";
  if (!cursor) renderIfActive();
  try {
    const page = await telemetryGet<SecurityPage>(searchPath(cursor));
    sec.rows = cursor ? [...sec.rows, ...page.rows] : page.rows;
    sec.next = page.next_cursor;
  } catch (error) {
    if (!cursor) sec.rows = [];
    sec.next = null;
    sec.error = errorMessage(error);
  } finally {
    sec.loading = false;
  }
  renderIfActive();
}

function renderIfActive(): void {
  if (state.currentRoute === "security") renderSecurityView();
}

export function securityStatus(): string {
  return `${sec.rows.length} security log line${sec.rows.length === 1 ? "" : "s"} loaded.`;
}

function renderForm(): string {
  const inputs = filterFields.map(f => `
      <label class="sec-field">
        <span>${filterLabels[f]}</span>
        <input type="${f === "status" ? "number" : "search"}" name="${f}" value="${escapeAttr(sec.filters[f] ?? "")}"
          autocomplete="off" spellcheck="false">
      </label>`).join("");
  const options = rangeOptions.map(([v, label]) =>
    `<option value="${v}"${v === sec.range ? " selected" : ""}>${label}</option>`).join("");
  const custom = sec.range === "custom" ? `
      <label class="sec-field"><span>From</span>
        <input type="datetime-local" name="customFrom" value="${escapeAttr(sec.customFrom)}"></label>
      <label class="sec-field"><span>To</span>
        <input type="datetime-local" name="customTo" value="${escapeAttr(sec.customTo)}"></label>` : "";
  return `
    <form class="sec-form" id="sec-form">
      <label class="sec-field"><span>Range</span><select name="range">${options}</select></label>
      ${custom}
      ${inputs}
      <div class="sec-form-actions">
        <button type="submit" class="btn-sm">Search</button>
        <button type="button" class="btn-sm" id="sec-reset">Reset</button>
      </div>
    </form>`;
}

function rawDetail(row: SecurityRow): string {
  let pretty = row.raw;
  try {
    pretty = JSON.stringify(JSON.parse(row.raw), null, 2);
  } catch {
    // Not JSON (journald, sshd): show the line as stored.
  }
  return pretty || "(no raw line stored)";
}

function renderRow(row: SecurityRow): string {
  const summary = row.message || [row.method, row.path].filter(Boolean).join(" ") || row.action;
  return `
    <tr class="sec-row" data-sec-id="${row.id}" tabindex="0" aria-expanded="false">
      <td class="sec-ts">${escapeHtml(formatTime(row.ts))}</td>
      <td>${escapeHtml(row.source)}</td>
      <td>${escapeHtml(row.host)}</td>
      <td>${escapeHtml(row.kind)}</td>
      <td class="sec-mono">${escapeHtml(row.src_ip)}</td>
      <td>${escapeHtml(row.user)}</td>
      <td class="volume-num">${row.status ? row.status : ""}</td>
      <td class="sec-summary">${escapeHtml(summary)}</td>
    </tr>
    <tr class="sec-raw-row hidden" data-sec-raw="${row.id}">
      <td colspan="8"><pre class="sec-raw">${escapeHtml(rawDetail(row))}</pre></td>
    </tr>`;
}

function renderResults(): string {
  if (sec.error) {
    return `<div class="callout callout-error"><span>${escapeHtml(sec.error)}</span></div>`;
  }
  if (sec.loading && !sec.rows.length) return `<div class="empty">Searching…</div>`;
  if (!sec.rows.length) return `<div class="empty">No security log lines match this search.</div>`;
  const more = sec.next
    ? `<button type="button" class="btn-sm" id="sec-more"${sec.loading ? " disabled" : ""}>Load more</button>`
    : `<span class="muted">End of results.</span>`;
  return `
    <div class="table-scroll">
      <table class="volume-table sec-table">
        <thead><tr><th>Time</th><th>Source</th><th>Host</th><th>Kind</th><th>Source IP</th><th>User</th>
          <th class="volume-num">Status</th><th>Message</th></tr></thead>
        <tbody>${sec.rows.map(renderRow).join("")}</tbody>
      </table>
    </div>
    <div class="sec-more">${more}</div>`;
}

export function renderSecurityView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Security";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = `
    <div class="view-head"><h2>Security logs</h2></div>
    <div class="section">${renderForm()}</div>
    <div class="section">${renderResults()}</div>`;
  wireSecurityView();
}

function readForm(form: HTMLFormElement): void {
  const data = new FormData(form);
  sec.range = String(data.get("range") ?? sec.range);
  sec.customFrom = String(data.get("customFrom") ?? sec.customFrom);
  sec.customTo = String(data.get("customTo") ?? sec.customTo);
  for (const f of filterFields) sec.filters[f] = String(data.get(f) ?? "");
}

function toggleRaw(row: HTMLElement): void {
  const id = row.getAttribute("data-sec-id");
  const detail = id ? elements.overviewView.querySelector(`[data-sec-raw="${CSS.escape(id)}"]`) : null;
  if (!detail) return;
  const open = detail.classList.toggle("hidden") === false;
  row.setAttribute("aria-expanded", String(open));
}

function wireSecurityView(): void {
  const form = document.getElementById("sec-form") as HTMLFormElement | null;
  form?.addEventListener("submit", (ev) => {
    ev.preventDefault();
    readForm(form);
    setStatus("Searching security logs…");
    void loadSecurity().then(() => setStatus(securityStatus()));
  });
  form?.querySelector<HTMLSelectElement>("select[name=range]")?.addEventListener("change", () => {
    readForm(form);
    renderSecurityView();
  });
  document.getElementById("sec-reset")?.addEventListener("click", () => {
    sec.filters = {};
    sec.range = "86400000";
    void loadSecurity();
  });
  document.getElementById("sec-more")?.addEventListener("click", () => {
    void fetchPage(sec.next).then(() => setStatus(securityStatus()));
  });
  elements.overviewView.querySelectorAll<HTMLElement>(".sec-row").forEach((row) => {
    row.addEventListener("click", () => toggleRaw(row));
    row.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        toggleRaw(row);
      }
    });
  });
}
