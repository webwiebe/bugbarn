// Logs view: REST loading, the live SSE stream, rendering, and row wiring.
import { renderLogRow, renderLogsViewMarkup } from "./components.js";
import type { ApiLogEntry } from "./types.js";
import { apiUrl, elements, setActiveView, state } from "./core.js";
import { fetchJson } from "./http.js";

export async function loadLogs(): Promise<void> {
  try {
    const params = new URLSearchParams();
    if (state.logLevel) {
      params.set("level", state.logLevel);
    }
    if (state.logSearch) {
      params.set("q", state.logSearch);
    }
    params.set("limit", "200");
    const qs = params.toString();
    const payload = await fetchJson(`/api/v1/logs${qs ? `?${qs}` : ""}`, true);
    const raw = payload as Record<string, unknown> | null;
    state.logs = Array.isArray(raw?.["logs"]) ? (raw["logs"] as ApiLogEntry[]) : [];
    if (state.currentRoute === "logs") renderLogsView();
  } catch {
    state.logs = [];
    if (state.currentRoute === "logs") renderLogsView();
  }
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
        row.innerHTML = renderLogRow(entry);
        const newRow = row.firstElementChild as HTMLElement | null;
        if (newRow) {
          list.insertBefore(newRow, list.firstChild);
          wireLogRowClick(newRow);
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
  elements.overviewView.innerHTML = renderLogsViewMarkup(state.logs, state.logLevel, state.logSearch);
  wireLogsView();
}

function wireLogRowClick(row: HTMLElement): void {
  row.addEventListener("click", () => {
    row.classList.toggle("expanded");
  });
}

function wireLogsView(): void {
  const levelFilter = document.getElementById("log-level-filter") as HTMLSelectElement | null;
  const searchInput = document.getElementById("log-search") as HTMLInputElement | null;
  const clearBtn = document.getElementById("log-clear") as HTMLButtonElement | null;
  const list = document.getElementById("log-list");

  levelFilter?.addEventListener("change", () => {
    state.logLevel = levelFilter.value;
    state.logs = [];
    connectLogSSE();
    void loadLogs();
  });

  let debounceTimer: number | null = null;
  searchInput?.addEventListener("input", () => {
    if (debounceTimer !== null) {
      window.clearTimeout(debounceTimer);
    }
    debounceTimer = window.setTimeout(() => {
      debounceTimer = null;
      state.logSearch = searchInput.value.trim();
      state.logs = [];
      connectLogSSE();
      void loadLogs();
    }, 300);
  });

  clearBtn?.addEventListener("click", () => {
    state.logs = [];
    if (list) {
      list.innerHTML = `<div class="empty">No log entries yet. Connect a project to start streaming logs.</div>`;
    }
  });

  list?.querySelectorAll<HTMLElement>(".log-row").forEach((row) => {
    wireLogRowClick(row);
  });

  const dot = document.getElementById("log-live-dot");
  if (state.logSSE && state.logSSE.readyState === EventSource.OPEN) {
    dot?.classList.add("connected");
  }
}

const logLevelNums: Record<string, number> = { trace: 10, debug: 20, info: 30, warn: 40, error: 50, fatal: 60 };

function logLevelMinNum(levelName: string): number {
  return logLevelNums[levelName] ?? 0;
}
