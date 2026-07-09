// Hash routing: parses the location hash into the current route/selection,
// drives the immediate cached render, the network refresh, and the status line.
import type { SettingsTab } from "./types.js";
import {
  setActiveNav,
  setLoadingBar,
  setPageTitle,
  setRouteChip,
  setStatus,
  state,
} from "./core.js";
import { renderLogin } from "./http.js";
import { loadLiveEvents } from "./live.js";
import { loadIssues, renderIssuesView } from "./views-issues.js";
import { loadEventDetail, loadIssueDetail, setDetailLoading } from "./views-issue-detail.js";
import { loadReleaseDetail, loadReleases, renderReleasesView } from "./views-releases.js";
import { loadAlerts, renderAlertsView } from "./views-alerts.js";
import { connectLogSSE, disconnectLogSSE, loadLogs, renderLogsView } from "./views-logs.js";
import { loadSdkInfo, loadSettings, renderSettingsView } from "./views-settings.js";
import { renderAccountView } from "./views-account.js";

export function route(): void {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const [kind, id] = parts;
  state.selectedIssueId = null;
  state.selectedEventId = null;
  state.selectedReleaseId = null;

  if (kind === "issues" && id) {
    state.currentRoute = "issues";
    state.selectedIssueId = decodeURIComponent(id);
    setPageTitle("Issues");
    setRouteChip(`Issue ${state.selectedIssueId}`);
  } else if (kind === "events" && id) {
    state.currentRoute = "issues";
    state.selectedEventId = decodeURIComponent(id);
    setPageTitle("Issues");
    setRouteChip(`Event ${state.selectedEventId}`);
  } else if (kind === "releases" && id) {
    state.currentRoute = "releases";
    state.selectedReleaseId = decodeURIComponent(id);
    setPageTitle("Releases");
    setRouteChip("Release detail");
  } else if (kind === "releases") {
    state.currentRoute = "releases";
    setPageTitle("Releases");
    setRouteChip("Releases");
  } else if (kind === "alerts") {
    state.currentRoute = "alerts";
    setPageTitle("Alerts");
    setRouteChip("Alerts");
  } else if (kind === "logs") {
    state.currentRoute = "logs";
    setPageTitle("Logs");
    setRouteChip("Logs");
  } else if (kind === "account") {
    state.currentRoute = "account";
    setPageTitle("Account");
    setRouteChip("Account");
  } else if (kind === "settings") {
    state.currentRoute = "settings";
    const validTabs: SettingsTab[] = ["overview", "projects", "preferences", "keys", "system"];
    state.settingsTab = (validTabs.includes(id as SettingsTab) ? id : "overview") as SettingsTab;
    const subPageTitles: Record<string, string> = { projects: "Projects", preferences: "Preferences", keys: "API Keys", system: "System" };
    const subTitle = subPageTitles[state.settingsTab];
    setPageTitle(subTitle ? `Settings — ${subTitle}` : "Settings");
    setRouteChip(subTitle ?? "Settings");
  } else {
    state.currentRoute = "issues";
    setPageTitle("Issues");
    setRouteChip("Issues");
  }

  setActiveNav();
  // Render immediately with cached state so the view switches without waiting for the network.
  renderCurrentRoute();
}

export function setRouteStatus(): void {
  if (state.currentRoute === "issues") {
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
  } else if (state.currentRoute === "releases") {
    setStatus(`${state.releases.length} release${state.releases.length === 1 ? "" : "s"} loaded.`);
  } else if (state.currentRoute === "alerts") {
    setStatus(`${state.alerts.length} alert${state.alerts.length === 1 ? "" : "s"} configured.`);
  } else if (state.currentRoute === "logs") {
    setStatus("Log stream connected.");
  } else if (state.currentRoute === "settings") {
    setStatus("Settings loaded.");
  } else if (state.currentRoute === "account") {
    setStatus("Account.");
  }
}

function renderCurrentRoute(): void {
  setRouteStatus();
  if (state.currentRoute === "releases") {
    renderReleasesView();
    if (state.selectedReleaseId && state.releases.length) {
      void loadReleaseDetail(state.selectedReleaseId);
    }
  } else if (state.currentRoute === "alerts") {
    renderAlertsView();
  } else if (state.currentRoute === "logs") {
    renderLogsView();
  } else if (state.currentRoute === "settings") {
    renderSettingsView();
  } else if (state.currentRoute === "account") {
    renderAccountView();
  } else if (state.selectedEventId) {
    setDetailLoading(`Event ${state.selectedEventId}`);
  } else if (state.selectedIssueId) {
    setDetailLoading(`Issue ${state.selectedIssueId}`);
  } else {
    renderIssuesView();
  }
}

export async function refreshAll(): Promise<void> {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }

  setLoadingBar(true);
  try {
    const tasks: Promise<void>[] = [loadIssues(), loadCurrentRouteData()];
    if (!state.selectedIssueId && !state.selectedEventId) {
      tasks.push(loadLiveEvents());
    }
    await Promise.all(tasks);
  } finally {
    setLoadingBar(false);
  }
}

async function loadCurrentRouteData(): Promise<void> {
  if (state.currentRoute !== "logs") {
    disconnectLogSSE();
  }
  setStatus("Refreshing…");
  if (state.currentRoute === "releases") {
    await loadReleases();
    if (state.selectedReleaseId) {
      await loadReleaseDetail(state.selectedReleaseId);
    }
    setRouteStatus();
    return;
  }
  if (state.currentRoute === "alerts") {
    await loadAlerts();
    setRouteStatus();
    return;
  }
  if (state.currentRoute === "logs") {
    await loadLogs();
    connectLogSSE();
    setRouteStatus();
    return;
  }
  if (state.currentRoute === "settings") {
    await loadSettings();
    loadSdkInfo();
    setRouteStatus();
    return;
  }
  if (state.currentRoute === "account") {
    // The hosted <iambarn-profile> element loads its own data; nothing to fetch.
    setRouteStatus();
    return;
  }
  if (state.selectedEventId) {
    await loadEventDetail(state.selectedEventId);
    return;
  }
  if (state.selectedIssueId) {
    await loadIssueDetail(state.selectedIssueId);
    return;
  }
  renderIssuesView();
}
