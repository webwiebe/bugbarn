// Logs view: the explorer (filters in the URL, volume chart, facets), the live
// SSE stream, rendering, and row wiring.
import { renderLogRow, renderLogsViewMarkup } from "./components.js";
import type { LogExplorerPanels } from "./components/log-explorer.js";
import {
  liveCompatible, logFiltersHash, logQueryParams, parseLogFilters, withAttr, withoutAttr,
  type LogFilters,
} from "./components/log-filters.js";
import { fetchSpanBarnRuntime } from "./spanbarn.js";
import type { ApiLogEntry, ApiLogFacets, ApiLogHistogram } from "./types.js";
import { apiUrl, elements, setActiveView, state } from "./core.js";
import { fetchJson } from "./http.js";

const emptyPanels: LogExplorerPanels = { histogram: null, facetKeys: null, facetValues: null };
let panels: LogExplorerPanels = emptyPanels;
let traceBase = "";
let traceBaseLoaded = false;

/** currentFilters reads the explorer state from the URL, the one place it lives. */
function currentFilters(): LogFilters {
  return parseLogFilters(location.hash);
}

async function loadTraceBase(): Promise<void> {
  if (traceBaseLoaded) return;
  traceBaseLoaded = true;
  traceBase = (await fetchSpanBarnRuntime())?.ui_url ?? "";
}

async function fetchPanel<T>(path: string, params: URLSearchParams): Promise<T | null> {
  try {
    return (await fetchJson(`${path}?${params.toString()}`, true)) as T | null;
  } catch {
    return null;
  }
}

async function loadPanels(filters: LogFilters, now: number): Promise<LogExplorerPanels> {
  const params = logQueryParams(filters, now);
  const valueParams = new URLSearchParams(params);
  if (filters.facetKey) valueParams.set("key", filters.facetKey);
  const [histogram, facetKeys, facetValues] = await Promise.all([
    fetchPanel<ApiLogHistogram>("/api/v1/logs/histogram", params),
    fetchPanel<ApiLogFacets>("/api/v1/logs/facets", params),
    filters.facetKey ? fetchPanel<ApiLogFacets>("/api/v1/logs/facets", valueParams) : Promise.resolve(null),
  ]);
  return { histogram, facetKeys, facetValues };
}

export async function loadLogs(): Promise<void> {
  const filters = currentFilters();
  state.logLevel = filters.level;
  state.logSearch = filters.q;
  const now = Date.now();
  try {
    const params = logQueryParams(filters, now);
    params.set("limit", "200");
    const [payload, nextPanels] = await Promise.all([
      fetchJson(`/api/v1/logs?${params.toString()}`, true),
      loadPanels(filters, now),
      loadTraceBase(),
    ]);
    const raw = payload as Record<string, unknown> | null;
    state.logs = Array.isArray(raw?.["logs"]) ? (raw["logs"] as ApiLogEntry[]) : [];
    panels = nextPanels;
    if (state.currentRoute === "logs") renderLogsView();
  } catch {
    state.logs = [];
    panels = emptyPanels;
    if (state.currentRoute === "logs") renderLogsView();
  }
}

/** applyFilters writes the new filters to the URL and reloads the view. The
 * hash is replaced in place, so changing a filter adds no history entry and
 * does not run the router. */
function applyFilters(next: LogFilters): void {
  history.replaceState(null, "", logFiltersHash(next));
  state.logs = [];
  connectLogSSE();
  void loadLogs();
}

export function connectLogSSE(): void {
  disconnectLogSSE();
  const url = apiUrl("/api/v1/logs/stream");
  const source = new EventSource(url, { withCredentials: true });
  state.logSSE = source;

  source.onopen = () => {
    const dot = document.getElementById("log-live-dot");
    dot?.classList.add("connected");
  };

  source.onmessage = (ev: MessageEvent) => {
    try {
      const entry = JSON.parse(ev.data as string) as ApiLogEntry;
      // EventSource can't send headers — stream is always all-projects, all-levels.
      // Apply the same filters the REST endpoint would apply.
      if (state.currentProject !== "__all" && entry.project_slug && entry.project_slug !== state.currentProject) {
        return;
      }
      if (!liveCompatible(currentFilters())) {
        return;
      }
      if (state.logLevel && entry.level_num < logLevelMinNum(state.logLevel)) {
        return;
      }
      if (state.logSearch && !entry.message.toLowerCase().includes(state.logSearch.toLowerCase())) {
        return;
      }
      state.logs = [entry, ...state.logs].slice(0, 500);
      const list = document.getElementById("log-list");
      if (list) {
        const empty = list.querySelector(".empty");
        if (empty) empty.remove();
        const row = document.createElement("div");
        row.innerHTML = renderLogRow(entry, traceBase);
        const newRow = row.firstElementChild as HTMLElement | null;
        if (newRow) {
          list.insertBefore(newRow, list.firstChild);
          wireLogRow(newRow);
          if (list.children.length > 500) {
            list.removeChild(list.lastChild as Node);
          }
        }
      }
    } catch {
      // malformed SSE data — skip
    }
  };

  source.onerror = () => {
    const dot = document.getElementById("log-live-dot");
    dot?.classList.remove("connected");
  };
}

export function disconnectLogSSE(): void {
  if (state.logSSE) {
    state.logSSE.close();
    state.logSSE = null;
  }
}

export function renderLogsView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Logs";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderLogsViewMarkup(state.logs, currentFilters(), panels, traceBase);
  wireLogsView();
}

function wireLogRow(row: HTMLElement): void {
  row.addEventListener("click", () => {
    row.classList.toggle("expanded");
  });
  row.querySelectorAll<HTMLElement>(".log-pill").forEach((pill) => {
    const add = (ev: Event): void => {
      ev.stopPropagation();
      const key = pill.getAttribute("data-attr-k");
      if (key) applyFilters(withAttr(currentFilters(), key, pill.getAttribute("data-attr-v") ?? ""));
    };
    pill.addEventListener("click", add);
    pill.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        add(ev);
      }
    });
  });
  row.querySelector<HTMLElement>(".log-trace")?.addEventListener("click", (ev) => ev.stopPropagation());
}

function wireExplorer(): void {
  const view = elements.overviewView;
  view.querySelector<HTMLSelectElement>("#log-range")?.addEventListener("change", (ev) => {
    const value = (ev.target as HTMLSelectElement).value;
    if (value === "1h" || value === "24h" || value === "7d") {
      applyFilters({ ...currentFilters(), range: value, from: "", to: "" });
    }
  });
  view.querySelector("#log-range-reset")?.addEventListener("click", () => {
    applyFilters({ ...currentFilters(), from: "", to: "" });
  });
  view.querySelectorAll<HTMLElement>("[data-attr-remove]").forEach((chip) => {
    chip.addEventListener("click", () => {
      applyFilters(withoutAttr(currentFilters(), chip.getAttribute("data-attr-remove") ?? ""));
    });
  });
  view.querySelectorAll<HTMLElement>("[data-bucket-start]").forEach((bar) => {
    bar.addEventListener("click", () => {
      const start = Date.parse(bar.getAttribute("data-bucket-start") ?? "");
      const seconds = Number(bar.getAttribute("data-bucket-seconds"));
      if (Number.isNaN(start) || !(seconds > 0)) return;
      applyFilters({
        ...currentFilters(),
        from: new Date(start).toISOString(),
        to: new Date(start + seconds * 1000).toISOString(),
      });
    });
  });
  view.querySelectorAll<HTMLElement>("[data-facet-key]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const key = btn.getAttribute("data-facet-key") ?? "";
      const filters = currentFilters();
      applyFilters({ ...filters, facetKey: filters.facetKey === key ? "" : key });
    });
  });
  view.querySelectorAll<HTMLElement>("[data-facet-add-key]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const key = btn.getAttribute("data-facet-add-key") ?? "";
      applyFilters(withAttr(currentFilters(), key, btn.getAttribute("data-facet-add-value") ?? ""));
    });
  });
}

function wireLogsView(): void {
  const levelFilter = document.getElementById("log-level-filter") as HTMLSelectElement | null;
  const searchInput = document.getElementById("log-search") as HTMLInputElement | null;
  const clearBtn = document.getElementById("log-clear") as HTMLButtonElement | null;
  const list = document.getElementById("log-list");

  levelFilter?.addEventListener("change", () => {
    applyFilters({ ...currentFilters(), level: levelFilter.value });
  });

  let debounceTimer: number | null = null;
  searchInput?.addEventListener("input", () => {
    if (debounceTimer !== null) {
      window.clearTimeout(debounceTimer);
    }
    debounceTimer = window.setTimeout(() => {
      debounceTimer = null;
      applyFilters({ ...currentFilters(), q: searchInput.value.trim() });
    }, 300);
  });

  clearBtn?.addEventListener("click", () => {
    state.logs = [];
    if (list) {
      list.innerHTML = `<div class="empty">No log entries shown. New entries appear here as they arrive.</div>`;
    }
  });

  list?.querySelectorAll<HTMLElement>(".log-row").forEach((row) => {
    wireLogRow(row);
  });
  wireExplorer();

  const dot = document.getElementById("log-live-dot");
  if (state.logSSE && state.logSSE.readyState === EventSource.OPEN) {
    dot?.classList.add("connected");
  }
}

const logLevelNums: Record<string, number> = { trace: 10, debug: 20, info: 30, warn: 40, error: 50, fatal: 60 };

function logLevelMinNum(levelName: string): number {
  return logLevelNums[levelName] ?? 0;
}
