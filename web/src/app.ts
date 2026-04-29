import {
  renderAlertsViewMarkup,
  renderEmptyIssues,
  renderErrorDetailMarkup,
  renderEventDetailMarkup,
  renderIssueDetailMarkup,
  renderIssueListMarkup,
  renderLiveListMarkup,
  renderLogRow,
  renderLogsViewMarkup,
  renderReleaseDetailMarkup,
  renderReleasesViewMarkup,
  renderSettingsViewMarkup,
  renderSetupGuide,
} from "./components.js";
import { normalizeList, normalizeObject, readString } from "./data.js";
import { eventIssueId, eventTimestamp, eventTitle, firstIdentifier, issueTitle } from "./domain.js";
import { escapeHtml, errorMessage } from "./format.js";
import type { ApiAlert, ApiApiKey, ApiEvent, ApiIssue, ApiLogEntry, ApiProject, ApiRelease, ApiSettings, AppElements, AppState, IssueSort, IssueStatus, RawRecord } from "./types.js";

const httpUnauthorized = 401;
const liveWindowMinutes = 15;

const sidebarKey = "bugbarn_sidebar";
const projectKey = "bugbarn_project";
const envKey = "bugbarn_env";

const state: AppState = {
  authChecked: false,
  authRequired: false,
  authenticated: false,
  username: "",
  projects: [],
  currentProject: (() => { const v = localStorage.getItem(projectKey); return (v && v !== "default") ? v : "__all"; })(),
  currentEnv: localStorage.getItem(envKey) ?? "",
  currentRoute: "issues",
  issues: [],
  issueQuery: "",
  issueSort: "last_seen",
  issueStatus: "all",
  selectedIssueId: null,
  selectedEventId: null,
  selectedReleaseId: null,
  releases: [],
  alerts: [],
  settings: null,
  apiKeys: [],
  liveEvents: [],
  liveError: null,
  liveTimer: null,
  liveSource: null,
  liveReconnectDelay: 3000,
  liveConnected: false,
  inFlight: new Map<string, Promise<unknown>>(),
  logs: [],
  logLevel: "",
  logSearch: "",
  logSSE: null,
};

const elements: AppElements = {
  refreshAll: byId<HTMLButtonElement>("refresh-all"),
  overviewView: byId<HTMLElement>("overview-view"),
  detailView: byId<HTMLElement>("detail-view"),
  navLinks: document.querySelectorAll<HTMLAnchorElement>(".side-nav a"),
  issueCount: byId<HTMLElement>("issue-count"),
  issueFilter: byId<HTMLInputElement>("issue-filter"),
  issueList: byId<HTMLElement>("issue-list"),
  detailTitle: byId<HTMLElement>("detail-title"),
  detailBody: byId<HTMLElement>("detail-body"),
  liveList: byId<HTMLElement>("live-list"),
  liveStatus: byId<HTMLElement>("live-status"),
  routeChip: byId<HTMLElement>("route-chip"),
  statusText: byId<HTMLElement>("status-text"),
};

elements.issueFilter.value = state.issueQuery;

const appFrame = document.querySelector<HTMLElement>(".app-frame");
const bbBtn = document.getElementById("bb-btn") as HTMLButtonElement | null;
const bbMenu = document.getElementById("bb-menu") as HTMLElement | null;
const bbMenuUser = document.getElementById("bb-menu-user") as HTMLElement | null;
const bbLogout = document.getElementById("bb-logout") as HTMLButtonElement | null;
const sidebarToggle = document.getElementById("sidebar-toggle") as HTMLButtonElement | null;
const projectSelect = document.getElementById("project-select") as HTMLSelectElement | null;
const envSelect = document.getElementById("env-select") as HTMLSelectElement | null;

function applySidebarState(): void {
  const expanded = localStorage.getItem(sidebarKey) === "expanded";
  appFrame?.classList.toggle("sidebar-open", expanded);
  if (sidebarToggle) {
    sidebarToggle.textContent = expanded ? "‹" : "›";
    sidebarToggle.setAttribute("aria-label", expanded ? "Collapse sidebar" : "Expand sidebar");
  }
}

applySidebarState();

sidebarToggle?.addEventListener("click", () => {
  const isOpen = appFrame?.classList.toggle("sidebar-open") ?? false;
  localStorage.setItem(sidebarKey, isOpen ? "expanded" : "collapsed");
  if (sidebarToggle) {
    sidebarToggle.textContent = isOpen ? "‹" : "›";
    sidebarToggle.setAttribute("aria-label", isOpen ? "Collapse sidebar" : "Expand sidebar");
  }
});


function closeBBMenu(): void {
  bbMenu?.setAttribute("hidden", "");
  bbBtn?.setAttribute("aria-expanded", "false");
}

bbBtn?.addEventListener("click", (ev) => {
  ev.stopPropagation();
  const isHidden = bbMenu?.hasAttribute("hidden");
  if (isHidden) {
    bbMenu?.removeAttribute("hidden");
    bbBtn?.setAttribute("aria-expanded", "true");
  } else {
    closeBBMenu();
  }
});

document.addEventListener("click", closeBBMenu);

document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") {
    closeBBMenu();
    bbBtn?.focus();
  }
});

bbLogout?.addEventListener("click", () => {
  void logout();
});

const mobileMenuBtn = document.getElementById("mobile-menu-btn") as HTMLButtonElement | null;
const mobileSidebar = document.getElementById("sidebar") as HTMLElement | null;

function openMobileSidebar(): void {
  if (!mobileSidebar) return;
  appFrame?.classList.add("mobile-nav-open");
  if (mobileMenuBtn) {
    mobileMenuBtn.textContent = "✕";
    mobileMenuBtn.setAttribute("aria-expanded", "true");
    mobileMenuBtn.setAttribute("aria-label", "Close navigation");
  }
}

function closeMobileNav(): void {
  appFrame?.classList.remove("mobile-nav-open");
  if (mobileMenuBtn) {
    mobileMenuBtn.textContent = "☰";
    mobileMenuBtn.setAttribute("aria-expanded", "false");
    mobileMenuBtn.setAttribute("aria-label", "Open navigation");
  }
}

mobileMenuBtn?.addEventListener("click", () => {
  const isOpen = appFrame?.classList.contains("mobile-nav-open") ?? false;
  if (isOpen) { closeMobileNav(); } else { openMobileSidebar(); }
});

document.querySelectorAll<HTMLAnchorElement>(".side-nav a").forEach((link) => {
  link.addEventListener("click", closeMobileNav);
});

projectSelect?.addEventListener("change", () => {
  const slug = projectSelect.value;
  state.currentProject = slug;
  localStorage.setItem(projectKey, slug);
  state.currentEnv = "";
  localStorage.removeItem(envKey);
  if (slug === "__all") {
    renderEnvSwitcher([]);
    void refreshAll();
  } else {
    void Promise.all([loadEnvironments(), refreshAll()]);
  }
});

envSelect?.addEventListener("change", () => {
  const env = envSelect.value;
  state.currentEnv = env;
  localStorage.setItem(envKey, env);
  void loadIssues();
});

async function logout(): Promise<void> {
  try {
    await fetch(apiUrl("/api/v1/logout"), { method: "POST", credentials: "include" });
  } catch {
    // ignore network errors on logout
  }
  state.authenticated = false;
  state.username = "";
  stopLiveStream();
  renderLogin();
}

function updateBBMenuUser(): void {
  if (bbMenuUser) {
    bbMenuUser.textContent = state.username || "BugBarn";
  }
}

elements.refreshAll.addEventListener("click", () => {
  void refreshAll();
});

window.addEventListener("hashchange", () => {
  route();
  void refreshAll();
});
window.addEventListener("beforeunload", stopLiveStream);

void start();

async function start(): Promise<void> {
  await loadSession();
  updateBBMenuUser();
  route();
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }
  const envLoad = state.currentProject !== "__all" ? loadEnvironments() : (renderEnvSwitcher([]), Promise.resolve());
  await Promise.all([loadProjects(), envLoad, refreshAll()]);
  initInstallPrompt();
}

// PWA install prompt — shown once until dismissed, never shown again after
// the user installs or explicitly dismisses it.
function initInstallPrompt(): void {
  if (localStorage.getItem("pwa_prompt_dismissed")) return;

  let deferredPrompt: Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: string }> } | null = null;

  window.addEventListener("beforeinstallprompt", (e) => {
    e.preventDefault();
    deferredPrompt = e as typeof deferredPrompt;
    showInstallBanner(async () => {
      if (!deferredPrompt) return;
      await deferredPrompt.prompt();
      const { outcome } = await deferredPrompt.userChoice;
      if (outcome === "accepted") dismissInstallBanner();
      deferredPrompt = null;
    });
  });

  // Also hide the banner once the app is actually installed
  window.addEventListener("appinstalled", () => dismissInstallBanner());
}

function showInstallBanner(onInstall: () => void): void {
  const existing = document.getElementById("pwa-install-banner");
  if (existing) return;

  const banner = document.createElement("div");
  banner.id = "pwa-install-banner";
  banner.setAttribute("role", "banner");
  banner.style.cssText = [
    "position:fixed", "bottom:16px", "right:16px", "z-index:1000",
    "display:flex", "align-items:center", "gap:10px",
    "background:#161b22", "border:1px solid #21262d",
    "border-radius:8px", "padding:12px 14px",
    "font-size:13px", "color:#c9d1d9",
    "box-shadow:0 4px 16px rgba(0,0,0,.5)",
    "max-width:320px",
  ].join(";");

  banner.innerHTML = `
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#d4a054" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
    </svg>
    <span>Install BugBarn as an app</span>
    <button id="pwa-install-btn" style="background:#d4a054;color:#0f1117;border:none;border-radius:4px;padding:4px 10px;font-size:12px;font-weight:600;cursor:pointer;white-space:nowrap">Install</button>
    <button id="pwa-dismiss-btn" aria-label="Dismiss" style="background:none;border:none;color:#8b949e;cursor:pointer;padding:2px 4px;font-size:16px;line-height:1">×</button>
  `;

  document.body.appendChild(banner);

  document.getElementById("pwa-install-btn")?.addEventListener("click", () => {
    onInstall();
  });
  document.getElementById("pwa-dismiss-btn")?.addEventListener("click", () => {
    dismissInstallBanner();
  });
}

function dismissInstallBanner(): void {
  localStorage.setItem("pwa_prompt_dismissed", "1");
  document.getElementById("pwa-install-banner")?.remove();
}

function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element as T;
}

function apiUrl(path: string): string {
  return path;
}

function setStatus(message: string): void {
  elements.statusText.textContent = message;
}

function setRouteChip(message: string, tone = ""): void {
  elements.routeChip.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.routeChip.textContent = message;
}

function setLiveStatus(message: string, tone = ""): void {
  elements.liveStatus.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.liveStatus.textContent = message;
}

function setActiveNav(): void {
  const routeGroup = state.currentRoute;
  elements.navLinks.forEach((link) => {
    const target = link.getAttribute("data-route") || "";
    link.classList.toggle("active", target === routeGroup);
  });
  document.querySelectorAll<HTMLAnchorElement>(".mobile-tab-bar a[data-route]").forEach((link) => {
    const target = link.getAttribute("data-route") || "";
    link.classList.toggle("active", target === routeGroup);
  });
}

function setPageTitle(title: string): void {
  const h1 = document.getElementById("topbar-title");
  if (h1) h1.textContent = title;
  document.title = `${title} — BugBarn`;
}

function route(): void {
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
  } else if (kind === "settings") {
    state.currentRoute = "settings";
    setPageTitle("Settings");
    setRouteChip("Settings");
  } else {
    state.currentRoute = "issues";
    setPageTitle("Issues");
    setRouteChip("Issues");
  }

  setActiveNav();
  // Render immediately with cached state so the view switches without waiting for the network.
  renderCurrentRoute();
}

function renderCurrentRoute(): void {
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
  } else if (state.selectedEventId) {
    setDetailLoading(`Event ${state.selectedEventId}`);
  } else if (state.selectedIssueId) {
    setDetailLoading(`Issue ${state.selectedIssueId}`);
  } else {
    renderIssuesView();
  }
}

function setLoadingBar(active: boolean): void {
  let bar = document.getElementById("loading-bar");
  if (active) {
    if (!bar) {
      bar = document.createElement("div");
      bar.id = "loading-bar";
      bar.style.cssText = "position:fixed;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,#d4a054 0%,#f0c070 50%,#d4a054 100%);background-size:200% 100%;animation:loadbar 1.2s linear infinite;z-index:9999;pointer-events:none";
      const style = document.createElement("style");
      style.textContent = "@keyframes loadbar{0%{background-position:0 0}100%{background-position:200% 0}}";
      document.head.appendChild(style);
      document.body.appendChild(bar);
    }
  } else {
    bar?.remove();
  }
}

async function refreshAll(): Promise<void> {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }

  setLoadingBar(true);
  try {
    await Promise.all([loadIssues(), loadLiveEvents(), loadCurrentRouteData()]);
  } finally {
    setLoadingBar(false);
  }
}

async function loadSession(): Promise<void> {
  try {
    const response = await fetch(apiUrl("/api/v1/me"), {
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    state.authChecked = true;
    if (response.status === httpUnauthorized) {
      state.authRequired = true;
      state.authenticated = false;
      return;
    }
    if (!response.ok) {
      state.authRequired = false;
      state.authenticated = true;
      return;
    }
    const payload = normalizeObject<RawRecord>(await response.json());
    state.authRequired = Boolean(payload.authEnabled);
    state.authenticated = Boolean(payload.authenticated);
    state.username = readString(payload, ["username"]);
  } catch {
    state.authChecked = true;
    state.authRequired = false;
    state.authenticated = true;
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
    return;
  }
  if (state.currentRoute === "alerts") {
    await loadAlerts();
    return;
  }
  if (state.currentRoute === "logs") {
    await loadLogs();
    connectLogSSE();
    return;
  }
  if (state.currentRoute === "settings") {
    await loadSettings();
    loadSdkInfo();
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

async function loadIssues(): Promise<void> {
  try {
    const params = new URLSearchParams();
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
    state.issues = normalizeList<ApiIssue>(payload, "issues");
    setStatus(`${state.issues.length} issue${state.issues.length === 1 ? "" : "s"} loaded.`);
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      renderIssuesView();
    }
  } catch (error) {
    state.issues = [];
    if (state.currentRoute === "issues" && !state.selectedIssueId && !state.selectedEventId) {
      renderIssuesView(error);
    }
    setStatus(`Issues unavailable: ${errorMessage(error)}`);
  }
}

async function loadReleases(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/releases", true);
    state.releases = payload ? normalizeList<ApiRelease>(payload, "releases") : [];
    renderReleasesView();
  } catch (error) {
    state.releases = [];
    renderReleasesView(error);
  }
}

async function loadReleaseDetail(releaseId: string): Promise<void> {
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

  const releaseStart = toTimestampMs(release.ObservedAt ?? release.observedAt ?? release.observed_at ?? release.createdAt ?? release.created_at ?? release.CreatedAt);
  const releaseEnd = nextRelease
    ? toTimestampMs(nextRelease.ObservedAt ?? nextRelease.observedAt ?? nextRelease.observed_at ?? nextRelease.createdAt ?? nextRelease.created_at ?? nextRelease.CreatedAt)
    : Date.now();

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

  const newIssues = allIssues.filter((issue) => {
    const fs = toTimestampMs(issue.FirstSeen ?? issue.firstSeen ?? issue.first_seen);
    return fs >= releaseStart && (releaseEnd === 0 || fs < releaseEnd);
  });

  const regressions = allIssues.filter((issue) => {
    const lr = toTimestampMs(issue.LastRegressedAt ?? (issue as Record<string, unknown>)["last_regressed_at"]);
    return lr > 0 && lr >= releaseStart && (releaseEnd === 0 || lr < releaseEnd);
  });

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

async function loadAlerts(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/alerts", true);
    state.alerts = payload ? normalizeList<ApiAlert>(payload, "alerts") : [];
    renderAlertsView();
  } catch (error) {
    state.alerts = [];
    renderAlertsView(error);
  }
}

async function loadSettings(): Promise<void> {
  try {
    const [settingsPayload, keysPayload] = await Promise.all([
      fetchJson("/api/v1/settings", true),
      fetchJson("/api/v1/apikeys", true).catch(() => null),
    ]);
    state.settings = settingsPayload ? normalizeObject<ApiSettings>(settingsPayload, "settings") : null;
    state.apiKeys = keysPayload ? normalizeList<ApiApiKey>(keysPayload as Record<string, unknown>, "apiKeys") : [];
    renderSettingsView();
  } catch (error) {
    state.settings = null;
    renderSettingsView(error);
  }
}

async function loadSdkInfo(): Promise<void> {
  const el = document.getElementById("sdk-info");
  if (!el) return;
  try {
    const res = await fetch("/packages/typescript/latest.json");
    if (!res.ok) throw new Error(`${res.status}`);
    const info = await res.json() as { version: string; filename: string; url: string };
    const absUrl = `${window.location.origin}${info.url}`;
    el.innerHTML = `
      <div class="kv"><span>Version</span><span>${escapeHtml(info.version)}</span></div>
      <div class="kv"><span>Tarball URL</span><code class="sdk-url">${escapeHtml(absUrl)}</code></div>
      <div class="kv"><span>Install</span><code class="sdk-url">pnpm add ${escapeHtml(absUrl)}</code></div>
    `;
  } catch {
    el.innerHTML = `<div class="kv"><span>Status</span><span>Package not yet published — deploy the web container first.</span></div>`;
  }
}

async function loadProjects(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/projects", true);
    state.projects = payload ? normalizeList<ApiProject>(payload, "projects") : [];
  } catch {
    state.projects = [];
  }
  renderProjectSwitcher();
}

function renderProjectSwitcher(): void {
  if (!projectSelect) return;
  const current = state.currentProject;
  const allSelected = (current === "__all" || !current) ? ' selected' : '';
  projectSelect.innerHTML = `<option value="__all"${allSelected}>All projects</option>` +
    state.projects
      .map((p) => {
        const slug = String(p.slug ?? p.Slug ?? "default");
        const name = String(p.name ?? p.Name ?? slug);
        const selected = slug === current ? ' selected' : '';
        return `<option value="${escapeHtml(slug)}"${selected}>${escapeHtml(name)}</option>`;
      })
      .join("");
  projectSelect.hidden = false;
}

async function loadEnvironments(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/facets/attributes.environment", true);
    const raw = payload as Record<string, unknown>;
    const envs = Array.isArray(raw?.["values"]) ? (raw["values"] as string[]) : [];
    renderEnvSwitcher(envs);
  } catch {
    renderEnvSwitcher([]);
  }
}

function renderEnvSwitcher(envs: string[]): void {
  if (!envSelect) return;
  const current = state.currentEnv;
  envSelect.innerHTML = `<option value="">All environments</option>` +
    envs
      .map((e) => {
        const selected = e === current ? ' selected' : '';
        return `<option value="${escapeHtml(e)}"${selected}>${escapeHtml(e)}</option>`;
      })
      .join("");
  envSelect.hidden = envs.length === 0;
}

async function loadIssueDetail(issueId: string): Promise<void> {
  setDetailLoading(`Issue ${issueId}`);
  try {
    const [issuePayload, eventsPayload] = await Promise.all([
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
      fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25`),
    ]);
    const issue = normalizeObject<ApiIssue>(issuePayload, "issue");
    const raw = eventsPayload as Record<string, unknown>;
    const events = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    renderIssueDetail(issue, events, hasMore);
  } catch (error) {
    renderErrorDetail(`Issue ${issueId}`, error);
  }
}

async function loadEventDetail(eventId: string): Promise<void> {
  setDetailLoading(`Event ${eventId}`);
  try {
    const eventPayload = await fetchJson(`/api/v1/events/${encodeURIComponent(eventId)}`);
    const event = normalizeObject<ApiEvent>(eventPayload, "event");
    let issue: ApiIssue | null = null;
    let issueEvents: ApiEvent[] = [];
    let eventsHasMore = false;

    const relatedIssueId = eventIssueId(event);
    if (relatedIssueId) {
      const issueId = String(relatedIssueId);
      try {
        const [issuePayload, eventsPayload] = await Promise.all([
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}`),
          fetchJson(`/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25`),
        ]);
        issue = normalizeObject<ApiIssue>(issuePayload, "issue");
        const eventsRaw = eventsPayload as Record<string, unknown>;
        issueEvents = normalizeList<ApiEvent>(eventsRaw, "events");
        eventsHasMore = Boolean(eventsRaw?.["hasMore"]);
      } catch {
        issueEvents = [];
      }
    }

    renderEventDetail(event, issue, issueEvents, eventsHasMore);
  } catch (error) {
    renderErrorDetail(`Event ${eventId}`, error);
  }
}

async function loadLiveEvents(): Promise<void> {
  startLiveStream();
}

function startLiveStream(): void {
  stopLiveStream();

  const since = new Date(Date.now() - liveWindowMinutes * 60 * 1000).toISOString();
  const url = `/api/v1/events/stream?since=${encodeURIComponent(since)}`;

  const source = new EventSource(url);
  state.liveSource = source;

  source.onopen = () => {
    state.liveConnected = true;
    state.liveReconnectDelay = 3000;
    state.liveError = null;
    setLiveStatus("Connected", "ok");
  };

  source.onmessage = (ev: MessageEvent) => {
    try {
      const event = JSON.parse(ev.data as string) as ApiEvent;
      state.liveEvents = [event, ...state.liveEvents].slice(0, 200);
      state.liveError = null;
      renderLiveList();
      setLiveStatus(`Live ${state.liveEvents.length}`, "warn");
    } catch {
      // malformed event data — skip
    }
  };

  source.onerror = () => {
    state.liveConnected = false;
    source.close();
    state.liveSource = null;
    setLiveStatus("Reconnecting", "warn");

    const delay = state.liveReconnectDelay;
    state.liveReconnectDelay = Math.min(delay * 2, 30000);
    state.liveTimer = window.setTimeout(() => {
      state.liveTimer = null;
      startLiveStream();
    }, delay);
  };
}

function stopLivePolling(): void {
  stopLiveStream();
}

function stopLiveStream(): void {
  if (state.liveSource) {
    state.liveSource.close();
    state.liveSource = null;
  }
  if (state.liveTimer) {
    window.clearTimeout(state.liveTimer);
    state.liveTimer = null;
  }
  state.liveConnected = false;
}

async function fetchJson(path: string, allowMissing = false): Promise<unknown> {
  const url = apiUrl(path);
  const existing = state.inFlight.get(url);
  if (existing) {
    return existing;
  }

  const headers: Record<string, string> = { Accept: "application/json" };
  if (state.currentProject && state.currentProject !== "default" && state.currentProject !== "__all") {
    headers["X-BugBarn-Project"] = state.currentProject;
  }
  const request = fetch(url, { credentials: "include", headers }).then(async (response) => {
    if (response.status === httpUnauthorized) {
      state.authRequired = true;
      state.authenticated = false;
      renderLogin();
    }
    if (allowMissing && response.status === 404) {
      return null;
    }
    if (!response.ok) {
      throw new Error(`${response.status} ${response.statusText}`.trim());
    }

    const text = await response.text();
    if (!text) {
      return null;
    }

    try {
      return JSON.parse(text) as unknown;
    } catch {
      return text;
    }
  });

  state.inFlight.set(url, request);
  try {
    return await request;
  } finally {
    state.inFlight.delete(url);
  }
}

function getCSRFToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)bugbarn_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

async function postJson(path: string, body: unknown): Promise<unknown> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  };
  if (csrf) {
    headers["X-BugBarn-CSRF"] = csrf;
  }
  const response = await fetch(apiUrl(path), {
    method: "POST",
    credentials: "include",
    headers,
    body: JSON.stringify(body),
  });
  if (response.status === httpUnauthorized) {
    state.authRequired = true;
    state.authenticated = false;
    renderLogin();
  }
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

async function deleteJson(path: string): Promise<unknown> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = { Accept: "application/json" };
  if (csrf) headers["X-BugBarn-CSRF"] = csrf;
  const response = await fetch(apiUrl(path), { method: "DELETE", credentials: "include", headers });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`.trim());
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

async function postFormData(path: string, formData: FormData): Promise<unknown> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = {};
  if (csrf) {
    headers["X-BugBarn-CSRF"] = csrf;
  }
  const response = await fetch(apiUrl(path), {
    method: "POST",
    credentials: "include",
    headers: Object.keys(headers).length ? headers : undefined,
    body: formData,
  });
  if (response.status === httpUnauthorized) {
    state.authRequired = true;
    state.authenticated = false;
    renderLogin();
  }
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

function renderIssuesView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Select an issue";
  elements.detailBody.innerHTML = "";
  const count = state.issues.length;
  elements.overviewView.innerHTML = `
    <div class="view-head">
      <div>
        <p class="eyebrow">Issues</p>
        <h2 id="issue-count">${escapeHtml(error ? "Unavailable" : `${count} issue${count === 1 ? "" : "s"}`)}</h2>
      </div>
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

  elements.issueList.innerHTML = renderIssueListMarkup(state.issues, "", state.selectedIssueId);
  elements.issueList.querySelectorAll("[data-issue-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const issueId = button.getAttribute("data-issue-id");
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });
}

function renderReleasesView(error: unknown = null): void {
  elements.overviewView.innerHTML = renderReleasesViewMarkup(state.releases, error);
  wireReleaseActions();
  if (!state.selectedReleaseId) {
    setActiveView("overview");
    elements.detailTitle.textContent = "Releases";
    elements.detailBody.innerHTML = "";
  }
}

function renderAlertsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Alerts";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderAlertsViewMarkup(state.alerts, error);
  wireAlertActions();
}

function renderSettingsView(error: unknown = null): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Settings";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = renderSettingsViewMarkup(state.settings, state.username, state.apiKeys, error, state.projects);
  wireSettingsActions();
}

function renderDetail(): void {
  if (state.authRequired && !state.authenticated) {
    renderLogin();
    return;
  }
  if (state.selectedEventId) {
    void loadEventDetail(state.selectedEventId);
    return;
  }
  if (state.selectedIssueId) {
    void loadIssueDetail(state.selectedIssueId);
    return;
  }

  setActiveView("overview");
}

function renderLogin(error = ""): void {
  stopLiveStream();
  appFrame?.classList.add("app-locked");
  setActiveView("detail");
  setRouteChip("Login", "warn");
  elements.issueCount.textContent = "Locked";
  elements.issueList.innerHTML = `<div class="empty">Log in to view issues.</div>`;
  elements.liveStatus.textContent = "Locked";
  elements.liveList.innerHTML = `<div class="empty">Live events require a session.</div>`;
  elements.detailTitle.textContent = "Log in";
  elements.detailBody.innerHTML = `
    <form class="section login-form" id="login-form">
      <p class="muted">Use the admin credentials configured for this BugBarn instance.</p>
      ${error ? `<div class="error">${escapeHtml(error)}</div>` : ""}
      <label class="field">
        <span>Username</span>
        <input name="username" type="text" autocomplete="username" required />
      </label>
      <label class="field">
        <span>Password</span>
        <input name="password" type="password" autocomplete="current-password" required />
      </label>
      <div class="link-row">
        <button type="submit">Log in</button>
      </div>
    </form>
  `;
  const form = document.getElementById("login-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    const formData = new FormData(form as HTMLFormElement);
    void login(String(formData.get("username") || ""), String(formData.get("password") || ""));
  });
}

async function login(username: string, password: string): Promise<void> {
  try {
    const response = await fetch(apiUrl("/api/v1/login"), {
      method: "POST",
      credentials: "include",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ username, password }),
    });
    if (!response.ok) {
      renderLogin("Invalid username or password.");
      return;
    }
    const payload = normalizeObject<RawRecord>(await response.json());
    state.authRequired = Boolean(payload.authEnabled);
    state.authenticated = Boolean(payload.authenticated);
    state.username = readString(payload, ["username"]);
    appFrame?.classList.remove("app-locked");
    updateBBMenuUser();
    setStatus(state.username ? `Logged in as ${state.username}.` : "Logged in.");
    await refreshAll();
  } catch (error) {
    renderLogin(errorMessage(error));
  }
}

function setDetailLoading(title: string): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = `<div class="loading">Loading.</div>`;
  setActiveView("detail");
}

function renderIssueDetail(issue: ApiIssue, events: ApiEvent[], hasMore = false): void {
  setActiveView("detail");
  elements.detailTitle.textContent = issueTitle(issue);
  elements.detailBody.innerHTML = renderIssueDetailMarkup(issue, events, state.releases, hasMore);
  wireIssueDetailActions(firstIdentifier(issue));

  if (hasMore) {
    const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
    btn?.addEventListener("click", () => {
      const oldestId = events.length > 0
        ? Number(String(events[0].ID ?? events[0].id ?? "0").replace(/\D/g, "")) || 0
        : 0;
      void loadOlderEvents(firstIdentifier(issue), oldestId, events);
    });
  }
}

async function loadOlderEvents(issueId: string, beforeRowId: number, existing: ApiEvent[]): Promise<void> {
  const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
  if (btn) btn.disabled = true;
  try {
    const payload = await fetchJson(
      `/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25&before=${beforeRowId}`
    );
    const raw = payload as Record<string, unknown>;
    const older = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    const combined = [...older, ...existing];
    renderIssueDetail(
      normalizeObject<ApiIssue>({ ID: issueId }, "issue"),
      combined,
      hasMore,
    );
    // Re-fetch the issue so the header stays accurate — use cached issues list.
    const cached = state.issues.find((i) => String(firstIdentifier(i)) === issueId);
    if (cached) {
      renderIssueDetail(cached, combined, hasMore);
    }
  } catch {
    if (btn) btn.disabled = false;
  }
}

async function loadOlderEventsForEvent(
  currentEvent: ApiEvent,
  issue: ApiIssue | null,
  issueId: string,
  beforeRowId: number,
  existing: ApiEvent[],
): Promise<void> {
  const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
  if (btn) btn.disabled = true;
  try {
    const payload = await fetchJson(
      `/api/v1/issues/${encodeURIComponent(issueId)}/events?limit=25&before=${beforeRowId}`
    );
    const raw = payload as Record<string, unknown>;
    const older = normalizeList<ApiEvent>(raw, "events");
    const hasMore = Boolean(raw?.["hasMore"]);
    const combined = [...older, ...existing];
    renderEventDetail(currentEvent, issue, combined, hasMore);
  } catch {
    if (btn) btn.disabled = false;
  }
}

function renderEventDetail(event: ApiEvent, issue: ApiIssue | null, issueEvents: ApiEvent[], hasMore = false): void {
  setActiveView("detail");
  const issueId = issue ? firstIdentifier(issue) : eventIssueId(event);
  elements.detailTitle.textContent = eventTitle(event);
  elements.detailBody.innerHTML = renderEventDetailMarkup(event, issue, issueEvents, hasMore, state.releases);
  wireEventDetailActions(issueId);

  if (hasMore && issueId) {
    const btn = elements.detailBody.querySelector<HTMLButtonElement>("[data-load-older]");
    btn?.addEventListener("click", () => {
      const oldestId = issueEvents.length > 0
        ? Number(String(issueEvents[0].ID ?? issueEvents[0].id ?? "0").replace(/\D/g, "")) || 0
        : 0;
      void loadOlderEventsForEvent(event, issue, issueId, oldestId, issueEvents);
    });
  }
}

function renderErrorDetail(title: string, error: unknown): void {
  elements.detailTitle.textContent = title;
  elements.detailBody.innerHTML = renderErrorDetailMarkup(error);
}

function wireIssueDetailActions(issueId: string): void {
  wireCopyButtons();

  elements.detailBody.querySelectorAll("[data-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });

  elements.detailBody.querySelectorAll("[data-resolve-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      const target = button.getAttribute("data-resolve-issue") || issueId;
      if (target) {
        void toggleIssueStatus(target, "resolved");
      }
    });
  });

  elements.detailBody.querySelectorAll("[data-reopen-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      const target = button.getAttribute("data-reopen-issue") || issueId;
      if (target) {
        void toggleIssueStatus(target, "unresolved");
      }
    });
  });

  elements.detailBody.querySelectorAll("[data-action='mute-issue']").forEach((button) => {
    button.addEventListener("click", () => {
      const id = (button as HTMLElement).dataset.issueId ?? issueId;
      const muteMode = (document.getElementById("mute-mode-select") as HTMLSelectElement | null)?.value ?? "until_regression";
      void muteIssue(id, muteMode);
    });
  });

  elements.detailBody.querySelectorAll("[data-action='unmute-issue']").forEach((button) => {
    button.addEventListener("click", () => {
      const id = (button as HTMLElement).dataset.issueId ?? issueId;
      void unmuteIssue(id);
    });
  });
}

function wireEventDetailActions(issueId: string): void {
  elements.detailBody.querySelectorAll("[data-open-issue]").forEach((button) => {
    button.addEventListener("click", () => {
      if (issueId) {
        location.hash = `#/issues/${encodeURIComponent(issueId)}`;
      }
    });
  });

  wireCopyButtons();

  elements.detailBody.querySelectorAll("[data-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const id = button.getAttribute("data-event-id");
      if (id) {
        location.hash = `#/events/${encodeURIComponent(id)}`;
      }
    });
  });
}

function wireCopyButtons(): void {
  elements.detailBody.querySelectorAll("[data-copy-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      const id = button.getAttribute("data-copy-id");
      if (!id) {
        return;
      }
      try {
        await navigator.clipboard.writeText(id);
        setStatus(`Copied ${id}.`);
      } catch {
        setStatus(`Could not copy ${id}.`);
      }
    });
  });
}

function wireReleaseActions(): void {
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

function wireAlertActions(): void {
  const form = elements.overviewView.querySelector<HTMLFormElement>("#alert-form");
  form?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitAlertForm(form);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-action='delete-alert']").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.dataset["id"];
      if (id) void deleteAlert(id);
    });
  });
}

async function deleteAlert(id: string): Promise<void> {
  try {
    await deleteJson(`/api/v1/alerts/${encodeURIComponent(id)}`);
    setStatus("Alert deleted.");
    await loadAlerts();
  } catch (error) {
    setStatus(`Failed to delete alert: ${errorMessage(error)}`);
  }
}

function wireSettingsActions(): void {
  const settingsForm = elements.overviewView.querySelector<HTMLFormElement>("#settings-form");
  const sourceMapForm = elements.overviewView.querySelector<HTMLFormElement>("#source-map-form");

  settingsForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSettingsForm(settingsForm);
  });

  sourceMapForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitSourceMapsForm(sourceMapForm);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-approve-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["approveProject"];
      if (slug) void approveProject(slug);
    });
  });
}

async function approveProject(slug: string): Promise<void> {
  try {
    await postJson(`/api/v1/projects/${encodeURIComponent(slug)}/approve`, {});
    setStatus(`Project ${slug} approved.`);
    await loadSettings();
  } catch (error) {
    setStatus(`Approve failed: ${errorMessage(error)}`);
  }
}

async function submitReleaseForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/releases", {
      name: String(data.get("name") || ""),
      environment: String(data.get("environment") || ""),
      observedAt: String(data.get("observedAt") || ""),
      version: String(data.get("version") || ""),
      commitSha: String(data.get("commitSha") || ""),
      url: String(data.get("url") || ""),
      notes: String(data.get("notes") || ""),
    });
    setStatus("Release marker saved.");
    await loadReleases();
  } catch (error) {
    setStatus(`Release marker unavailable: ${errorMessage(error)}`);
  }
}

async function submitAlertForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  const cooldownRaw = data.get("cooldown_minutes");
  try {
    await postJson("/api/v1/alerts", {
      name: String(data.get("name") || ""),
      condition: String(data.get("condition") || ""),
      webhook_url: String(data.get("webhook_url") || ""),
      cooldown_minutes: cooldownRaw ? Number(cooldownRaw) : undefined,
      enabled: data.get("enabled") !== null,
    });
    setStatus("Alert saved.");
    await loadAlerts();
  } catch (error) {
    setStatus(`Alert unavailable: ${errorMessage(error)}`);
  }
}

async function submitSettingsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postJson("/api/v1/settings", {
      displayName: String(data.get("displayName") || ""),
      timezone: String(data.get("timezone") || ""),
      defaultEnvironment: String(data.get("defaultEnvironment") || ""),
      liveWindowMinutes: Number(data.get("liveWindowMinutes") || liveWindowMinutes),
      stacktraceContextLines: Number(data.get("stacktraceContextLines") || 3),
    });
    setStatus("Settings saved.");
    await loadSettings();
  } catch (error) {
    setStatus(`Settings unavailable: ${errorMessage(error)}`);
  }
}

async function submitSourceMapsForm(form: HTMLFormElement): Promise<void> {
  const data = new FormData(form);
  try {
    await postFormData("/api/v1/source-maps", data);
    setStatus("Source maps uploaded.");
  } catch (error) {
    setStatus(`Source map upload unavailable: ${errorMessage(error)}`);
  }
}

async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
    ...(init.headers as Record<string, string> ?? {}),
  };
  if (csrf) {
    headers["X-BugBarn-CSRF"] = csrf;
  }
  if (state.currentProject && state.currentProject !== "default" && state.currentProject !== "__all") {
    headers["X-BugBarn-Project"] = state.currentProject;
  }
  return fetch(apiUrl(path), { credentials: "include", ...init, headers });
}

async function muteIssue(id: string, muteMode: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/issues/${encodeURIComponent(id)}/mute`, {
      method: "PATCH",
      body: JSON.stringify({ mute_mode: muteMode }),
    });
    if (res.ok) {
      setStatus(`Issue ${id} muted.`);
      await loadIssues();
      if (state.selectedIssueId === id) {
        await loadIssueDetail(id);
      }
    } else {
      setStatus(`Mute failed: ${res.status} ${res.statusText}`.trim());
    }
  } catch (error) {
    setStatus(`Mute unavailable: ${errorMessage(error)}`);
  }
}

async function unmuteIssue(id: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/issues/${encodeURIComponent(id)}/unmute`, { method: "PATCH" });
    if (res.ok) {
      setStatus(`Issue ${id} unmuted.`);
      await loadIssues();
      if (state.selectedIssueId === id) {
        await loadIssueDetail(id);
      }
    } else {
      setStatus(`Unmute failed: ${res.status} ${res.statusText}`.trim());
    }
  } catch (error) {
    setStatus(`Unmute unavailable: ${errorMessage(error)}`);
  }
}

async function toggleIssueStatus(issueId: string, status: "resolved" | "unresolved"): Promise<void> {
  try {
    await postJson(`/api/v1/issues/${encodeURIComponent(issueId)}/${status === "resolved" ? "resolve" : "reopen"}`, {});
    setStatus(`Issue ${issueId} marked ${status}.`);
    await loadIssueDetail(issueId);
    await loadIssues();
  } catch (error) {
    setStatus(`Issue status unavailable: ${errorMessage(error)}`);
  }
}

function renderLiveList(): void {
  elements.liveList.innerHTML = renderLiveListMarkup(state.liveEvents, state.liveError, state.releases);
  elements.liveList.querySelectorAll("[data-live-event-id]").forEach((button) => {
    button.addEventListener("click", () => {
      const eventId = button.getAttribute("data-live-event-id");
      if (eventId) {
        location.hash = `#/events/${encodeURIComponent(eventId)}`;
      }
    });
  });
}

async function loadLogs(): Promise<void> {
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
    renderLogsView();
  } catch {
    state.logs = [];
    renderLogsView();
  }
}

function connectLogSSE(): void {
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
      // EventSource can't send headers, so the stream is always all-projects.
      // Filter client-side when a specific project is selected.
      if (state.currentProject !== "__all" && entry.project_slug && entry.project_slug !== state.currentProject) {
        return;
      }
      state.logs = [entry, ...state.logs].slice(0, 500);
      const list = document.getElementById("log-list");
      if (list) {
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

function disconnectLogSSE(): void {
  if (state.logSSE) {
    state.logSSE.close();
    state.logSSE = null;
  }
}

function renderLogsView(): void {
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

function setActiveView(view: "overview" | "detail"): void {
  elements.overviewView.classList.toggle("hidden", view !== "overview");
  elements.detailView.classList.toggle("hidden", view !== "detail");
}

function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}




