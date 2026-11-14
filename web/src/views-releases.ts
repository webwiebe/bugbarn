// Releases view: list/detail loading, rendering, and the release-marker form.
import { renderReleaseDetailMarkup, renderReleasesViewMarkup } from "./components.js";
import { normalizeList } from "./records.js";
import { errorMessage, escapeHtml } from "./format.js";
import type { ApiIssue, ApiRelease } from "./types.js";
import { elements, setActiveView, setStatus, state, toTimestampMs } from "./core.js";
import { fetchJson, postJson } from "./http.js";

export async function loadReleases(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/releases", true);
    state.releases = payload ? normalizeList<ApiRelease>(payload, "releases") : [];
    if (state.currentRoute === "releases") renderReleasesView();
  } catch (error) {
    state.releases = [];
    if (state.currentRoute === "releases") renderReleasesView(error);
  }
}

function releaseTimestampMs(r: ApiRelease): number {
  return toTimestampMs(r.ObservedAt ?? r.observedAt ?? r.observed_at ?? r.createdAt ?? r.created_at ?? r.CreatedAt);
}

function regressedWithin(issue: ApiIssue, start: number, end: number): boolean {
  const lr = toTimestampMs(issue.LastRegressedAt ?? (issue as Record<string, unknown>)["last_regressed_at"]);
  return lr > 0 && lr >= start && (end === 0 || lr < end);
}

export async function loadReleaseDetail(releaseId: string): Promise<void> {
  const release = state.releases.find((r) => String(r.id ?? r.ID ?? "") === releaseId);
  if (!release) {
    setActiveView("detail");
    elements.detailTitle.textContent = "Release not found";
    elements.detailBody.innerHTML = `<div class="error">Release ${escapeHtml(releaseId)} not found. Try refreshing.</div>`;
    return;
  }

  const idx = state.releases.indexOf(release);
  // releases are newest-first; the next release chronologically is the one before this in the array
  const nextRelease = idx > 0 ? state.releases[idx - 1] : null;

  const releaseStart = releaseTimestampMs(release);
  const releaseEnd = nextRelease ? releaseTimestampMs(nextRelease) : Date.now();

  let allIssues = state.issues;
  // If issues list is empty or wasn't loaded yet, fetch without project filter to get all
  if (!allIssues.length) {
    try {
      const payload = await fetchJson("/api/v1/issues");
      allIssues = normalizeList<ApiIssue>(payload, "issues");
    } catch {
      allIssues = [];
    }
  }

  const newIssues = await loadNewIssues(releaseId, allIssues, releaseStart, releaseEnd);

  const regressions = allIssues.filter((issue) => regressedWithin(issue, releaseStart, releaseEnd));

  setActiveView("detail");
  elements.detailTitle.textContent = String(release.Name ?? release.name ?? "Release");
  elements.detailBody.innerHTML = renderReleaseDetailMarkup(release, newIssues, regressions, nextRelease);

  elements.detailBody.querySelectorAll<HTMLElement>("[data-issue-id]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset["issueId"];
      if (id) location.hash = `#/issues/${encodeURIComponent(id)}`;
    });
  });
}

// The server resolves the window per environment and reads the full issue table.
// The loaded list is only the fallback when the request fails.
async function loadNewIssues(releaseId: string, loaded: ApiIssue[], start: number, end: number): Promise<ApiIssue[]> {
  try {
    const params = new URLSearchParams({ status: "all", limit: "200", new_in_release: releaseId });
    const payload = await fetchJson(`/api/v1/issues?${params.toString()}`, true);
    if (payload) return normalizeList<ApiIssue>(payload, "issues");
  } catch {
    // fall through to the loaded list
  }
  return loaded.filter((issue) => {
    const fs = toTimestampMs(issue.FirstSeen ?? issue.firstSeen ?? issue.first_seen);
    return fs >= start && (end === 0 || fs < end);
  });
}

export function renderReleasesView(error: unknown = null): void {
  elements.overviewView.innerHTML = renderReleasesViewMarkup(state.releases, error, state.releasesEnvFilter);
  wireReleaseActions();
  if (!state.selectedReleaseId) {
    setActiveView("overview");
    elements.detailTitle.textContent = "Releases";
    elements.detailBody.innerHTML = "";
  }
}

function wireReleaseActions(): void {
  elements.overviewView.querySelectorAll<HTMLButtonElement>(".env-filter-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      state.releasesEnvFilter = btn.dataset["env"] ?? "";
      renderReleasesView();
    });
  });

  const form = elements.overviewView.querySelector<HTMLFormElement>("#release-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitReleaseForm(form);
  });

  elements.overviewView.querySelectorAll<HTMLElement>("[data-release-id]").forEach((card) => {
    const handleActivate = () => {
      const id = card.dataset["releaseId"];
      if (id) location.hash = `#/releases/${encodeURIComponent(id)}`;
    };
    card.addEventListener("click", handleActivate);
    card.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        handleActivate();
      }
    });
  });
}

async function submitReleaseForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  const name = String(data.get("name") || "");
  const environment = String(data.get("environment") || "");
  try {
    await postJson("/api/v1/releases", {
      name,
      environment,
      observedAt: String(data.get("observedAt") || ""),
      version: String(data.get("version") || ""),
      commitSha: String(data.get("commitSha") || ""),
      url: String(data.get("url") || ""),
      notes: String(data.get("notes") || ""),
    });
    window.funnelbarn?.track("release_created", { name, environment });
    setStatus("Release marker saved.");
    await loadReleases();
  } catch (error) {
    setStatus(`Release marker unavailable: ${errorMessage(error)}`);
  }
}
