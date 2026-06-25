// Issues list view: list loading/pagination, sparklines, and the overview
// rendering + wiring. Issue/event detail rendering lives in views-issue-detail.
import { renderEmptyIssues, renderIssueListMarkup, renderSetupGuide } from "./components.js";
import { normalizeList } from "./data.js";
import { errorMessage, escapeHtml } from "./format.js";
import type { ApiIssue, IssueSort, IssueStatus } from "./types.js";
import { byId, elements, setActiveView, setStatus, state } from "./core.js";
import { fetchJson } from "./http.js";
import { setRouteStatus } from "./router.js";

let issueLoadGeneration = 0;

const ISSUE_PAGE_SIZE = 50;

export async function loadIssues(): Promise<void> {
  const generation = ++issueLoadGeneration;
  try {
    const params = new URLSearchParams();
    params.set("limit", String(ISSUE_PAGE_SIZE));
    if (state.issueSort && state.issueSort !== "last_seen") {
      params.set("sort", state.issueSort);
    }
    if (state.issueStatus && state.issueStatus !== "all") {
      params.set("status", state.issueStatus);
    }
    if (state.issueQuery) {
      params.set("q", state.issueQuery);
    }
    if (state.currentEnv) {
      params.set("attributes.environment", state.currentEnv);
    }
    const qs = params.toString();
    const payload = await fetchJson(`/api/v1/issues${qs ? `?${qs}` : ""}`);
    if (generation !== issueLoadGeneration) return;
    const raw = payload as Record<string, unknown>;
    state.issues = normalizeList<ApiIssue>(raw, "issues");
    state.issueHasMore = Boolean(raw?.["hasMore"]);
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      setRouteStatus();
      renderIssuesView();
    }
    loadSparklines();
  } catch (error) {
    if (generation !== issueLoadGeneration) return;
    state.issues = [];
    state.issueHasMore = false;
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      renderIssuesView(error);
      setStatus(`Issues unavailable: ${errorMessage(error)}`);
    }
  }
}

export async function loadMoreIssues(): Promise<void> {
  if (!state.issueHasMore) return;
  const generation = issueLoadGeneration;
  try {
    const params = new URLSearchParams();
    params.set("limit", String(ISSUE_PAGE_SIZE));
    params.set("offset", String(state.issues.length));
    if (state.issueSort && state.issueSort !== "last_seen") {
      params.set("sort", state.issueSort);
    }
    if (state.issueStatus && state.issueStatus !== "all") {
      params.set("status", state.issueStatus);
    }
    if (state.issueQuery) {
      params.set("q", state.issueQuery);
    }
    if (state.currentEnv) {
      params.set("attributes.environment", state.currentEnv);
    }
    const qs = params.toString();
    const payload = await fetchJson(`/api/v1/issues?${qs}`);
    if (generation !== issueLoadGeneration) return;
    const raw = payload as Record<string, unknown>;
    const more = normalizeList<ApiIssue>(raw, "issues");
    state.issues = state.issues.concat(more);
    state.issueHasMore = Boolean(raw?.["hasMore"]);
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      setRouteStatus();
      renderIssuesView();
    }
    loadSparklines();
  } catch (error) {
    if (generation !== issueLoadGeneration) return;
    setStatus(`Failed to load more issues: ${errorMessage(error)}`);
  }
}

export async function loadSparklines(): Promise<void> {
  const generation = issueLoadGeneration;
  if (state.issues.length === 0) return;
  const ids = state.issues
    .map((i) => String(i.ID ?? i.id ?? ""))
    .filter(Boolean)
    .join(",");
  if (!ids) return;
  try {
    const payload = await fetchJson(`/api/v1/issues/sparklines?ids=${encodeURIComponent(ids)}`);
    if (generation !== issueLoadGeneration) return;
    const sparklines = (payload as Record<string, unknown>)?.["sparklines"] as Record<string, number[]> | undefined;
    if (sparklines) {
      for (const issue of state.issues) {
        const key = String(issue.ID ?? issue.id ?? "");
        if (key && sparklines[key]) {
          issue.hourly_counts = sparklines[key];
        }
      }
      if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
        renderIssuesView();
      }
    }
  } catch {
    // Sparklines are non-critical; silently ignore failures.
  }
}

export function renderIssuesView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Select an issue";
  elements.detailBody.innerHTML = "";
  const count = state.issues.length;
  elements.overviewView.innerHTML = `
    <div class="view-head">
      <h2 id="issue-count">${escapeHtml(error ? "Unavailable" : `${count} issue${count === 1 ? "" : "s"}`)}</h2>
      <div class="view-actions">
        <input id="issue-filter" type="search" placeholder="Search issues…" aria-label="Search issues" value="${escapeHtml(state.issueQuery)}" />
        <select id="issue-sort" aria-label="Sort issues">
          <option value="last_seen"${state.issueSort === "last_seen" ? " selected" : ""}>Last seen</option>
          <option value="first_seen"${state.issueSort === "first_seen" ? " selected" : ""}>First seen</option>
          <option value="event_count"${state.issueSort === "event_count" ? " selected" : ""}>Event count</option>
        </select>
      </div>
    </div>
    <div class="issue-status-tabs" role="tablist">
      <button class="tab${state.issueStatus === "all" ? " active" : ""}" data-status="all" role="tab">All</button>
      <button class="tab${state.issueStatus === "open" ? " active" : ""}" data-status="open" role="tab">Open</button>
      <button class="tab${state.issueStatus === "resolved" ? " active" : ""}" data-status="resolved" role="tab">Resolved</button>
      <button class="tab${state.issueStatus === "muted" ? " active" : ""}" data-status="muted" role="tab">Muted</button>
    </div>
    <div id="issue-list" class="list issue-list" aria-live="polite"></div>
  `;
  elements.issueCount = byId<HTMLElement>("issue-count");
  elements.issueFilter = byId<HTMLInputElement>("issue-filter");
  elements.issueList = byId<HTMLElement>("issue-list");

  elements.issueFilter.addEventListener("input", () => {
    state.issueQuery = elements.issueFilter.value.trim();
    void loadIssues();
  });

  byId<HTMLSelectElement>("issue-sort").addEventListener("change", (ev) => {
    state.issueSort = (ev.target as HTMLSelectElement).value as IssueSort;
    void loadIssues();
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-status]").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.issueStatus = (btn.getAttribute("data-status") ?? "all") as IssueStatus;
      void loadIssues();
    });
  });

  renderIssuesList(error);
}

function renderIssuesList(error: unknown = null): void {
  if (error) {
    elements.issueCount.textContent = "Unavailable";
    elements.issueList.innerHTML = renderIssueListMarkup([], "", state.selectedIssueId, error);
    return;
  }

  const count = state.issues.length;
  elements.issueCount.textContent = `${count} issue${count === 1 ? "" : "s"}`;

  if (!count) {
    elements.issueList.innerHTML = renderEmptyIssues(renderSetupGuide());
    return;
  }

  let html = renderIssueListMarkup(state.issues, "", state.selectedIssueId);
  if (state.issueHasMore) {
    html += `<button class="load-more-btn" id="load-more-issues" type="button">Load more issues</button>`;
  }
  elements.issueList.innerHTML = html;
  elements.issueList.querySelectorAll("[data-issue-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const issueId = button.getAttribute("data-issue-id");
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });
  const loadMoreBtn = document.getElementById("load-more-issues");
  if (loadMoreBtn) {
    loadMoreBtn.addEventListener("click", () => void loadMoreIssues());
  }
}
