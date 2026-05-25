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
import { eventIssueId, eventTitle, firstIdentifier, issueTitle } from "./domain.js";
import { escapeHtml, errorMessage } from "./format.js";
import type { ApiAlert, ApiAlias, ApiApiKey, ApiEvent, ApiIssue, ApiLogEntry, ApiProject, ApiProjectGroup, ApiRelease, ApiSettings, AppElements, AppState, IssueSort, IssueStatus, RawRecord, SettingsTab } from "./types.js";
import { initInstrumentation } from "./instrumentation.js";

initInstrumentation();

const httpUnauthorized = 401;
const liveWindowMinutes = 15;

const sidebarKey = "bugbarn_sidebar";
const projectKey = "bugbarn_project";
const envKey = "bugbarn_env";
const groupKey = "bugbarn_group";

const state: AppState = {
  authChecked: false,
  authRequired: false,
  authenticated: false,
  username: "",
  projects: [],
  groups: [],
  aliases: [],
  currentProject: (() => { const v = localStorage.getItem(projectKey); return (v && v !== "default") ? v : "__all"; })(),
  currentGroup: localStorage.getItem(groupKey) || null,
  settingsTab: "overview",
  currentEnv: localStorage.getItem(envKey) ?? "",
  currentRoute: "issues",
  issues: [],
  issueQuery: "",
  issueSort: "last_seen",
  issueStatus: "open",
  issueHasMore: false,
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
const loginScreen = document.getElementById("login-screen") as HTMLElement | null;
const loginForm = document.getElementById("login-form") as HTMLFormElement | null;
const loginError = document.getElementById("login-error") as HTMLElement | null;
const bbBtn = document.getElementById("bb-btn") as HTMLButtonElement | null;
const bbMenu = document.getElementById("bb-menu") as HTMLElement | null;
const bbMenuUser = document.getElementById("bb-menu-user") as HTMLElement | null;
const bbLogout = document.getElementById("bb-logout") as HTMLButtonElement | null;
const sidebarToggle = document.getElementById("sidebar-toggle") as HTMLButtonElement | null;
const envSelect = document.getElementById("env-select") as HTMLSelectElement | null;
const pickerBtn = document.getElementById("project-picker-btn") as HTMLButtonElement | null;
const pickerDropdown = document.getElementById("project-picker-dropdown") as HTMLElement | null;
const pickerFilter = document.getElementById("project-picker-filter") as HTMLInputElement | null;
const pickerList = document.getElementById("project-picker-list") as HTMLElement | null;

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

loginForm?.addEventListener("submit", (event) => {
  event.preventDefault();
  const formData = new FormData(loginForm);
  void login(String(formData.get("username") || ""), String(formData.get("password") || ""));
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

// Open/close picker
pickerBtn?.addEventListener("click", (e) => {
  e.stopPropagation();
  if (pickerDropdown?.hidden === false) {
    closePicker();
  } else {
    openPicker();
  }
});

document.addEventListener("click", (e) => {
  if (pickerDropdown && !pickerDropdown.hidden) {
    const picker = document.getElementById("project-picker");
    if (picker && !picker.contains(e.target as Node)) closePicker();
  }
});

pickerFilter?.addEventListener("input", () => {
  renderPickerList(pickerFilter.value);
});

pickerFilter?.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { closePicker(); pickerBtn?.focus(); }
  if (e.key === "ArrowDown") { (pickerList?.querySelector<HTMLButtonElement>(".picker-item") )?.focus(); e.preventDefault(); }
});

pickerList?.addEventListener("keydown", (e) => {
  const items = Array.from(pickerList.querySelectorAll<HTMLButtonElement>(".picker-item"));
  const idx = items.indexOf(document.activeElement as HTMLButtonElement);
  if (e.key === "ArrowDown" && idx < items.length - 1) { items[idx + 1].focus(); e.preventDefault(); }
  if (e.key === "ArrowUp") { if (idx > 0) items[idx - 1].focus(); else pickerFilter?.focus(); e.preventDefault(); }
  if (e.key === "Escape") { closePicker(); pickerBtn?.focus(); }
});

envSelect?.addEventListener("change", () => {
  const env = envSelect.value;
  state.currentEnv = env;
  localStorage.setItem(envKey, env);
  void loadIssues();
});

async function logout(): Promise<void> {
  // Snapshot before /api/v1/logout clears the hint cookie — if this session
  // came from iambarn we must also end the IdP session, otherwise the SPA's
  // login screen would auto-redirect back into iambarn, iambarn would reuse
  // its still-valid session, and the user would bounce straight back in.
  const wasOIDC = document.cookie.split("; ").some((c) => c.startsWith("bugbarn_auth_method=oidc"));
  try {
    await fetch(apiUrl("/api/v1/logout"), { method: "POST", credentials: "include" });
  } catch {
    // ignore network errors on logout
  }
  state.authenticated = false;
  state.username = "";
  stopLiveStream();
  if (wasOIDC) {
    const endURL = await fetchIAMBarnEndSessionURL();
    if (endURL) {
      window.location.assign(endURL);
      return;
    }
  }
  renderLogin();
}

async function fetchIAMBarnEndSessionURL(): Promise<string> {
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return "";
    const cfg = await res.json() as { oidc?: OIDCRuntime };
    return cfg?.oidc?.endSessionURL ?? "";
  } catch {
    return "";
  }
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
  void initFunnelBarn();
  void initSelfReporting();
  void initIAMBarnProfileLink();

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
  void checkPendingProjects();
  requestNotificationPermission();
}

// ---------------------------------------------------------------------------
// FunnelBarn analytics (opt-in)
//
// The Go server exposes GET /api/v1/runtime-config. When
// BUGBARN_FUNNELBARN_ENDPOINT is set, it returns:
//   { "funnelbarn": { "enabled": true, "endpoint": "...", "apiKey": "..." } }
//
// We dynamically inject the FunnelBarn JS SDK from:
//   {endpoint}/sdk/funnelbarn.js
// (FunnelBarn serves its pre-built IIFE bundle at that path via the web
// container's nginx static file server — see web/public/ in the FunnelBarn
// repo, or build sdks/js and place the output there.)
//
// After the script loads, window.funnelbarn is available and we call:
//   window.funnelbarn.init({ apiKey, endpoint })
//   window.funnelbarn.page()
// ---------------------------------------------------------------------------

// TypeScript type declaration for the globally-injected FunnelBarn SDK.
declare global {
  interface Window {
    funnelbarn?: {
      init(options: { apiKey: string; endpoint: string }): void;
      page(): void;
      track(name: string, properties?: Record<string, unknown>): void;
    };
  }
}

async function initFunnelBarn(): Promise<void> {
  let cfg: { funnelbarn?: { enabled: boolean; endpoint?: string; apiKey?: string } };
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return;
    cfg = await res.json() as typeof cfg;
  } catch {
    // Non-critical — silently abort if the endpoint is unreachable.
    return;
  }

  const fb = cfg?.funnelbarn;
  if (!fb?.enabled || !fb.endpoint || !fb.apiKey) return;

  const { endpoint, apiKey } = fb;

  // Inject the SDK script tag. FunnelBarn serves the pre-built IIFE bundle at
  // {endpoint}/sdk/funnelbarn.js from the web container's nginx static server.
  await new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = `${endpoint}/sdk/funnelbarn.js`;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`funnelbarn: failed to load SDK from ${script.src}`));
    document.head.appendChild(script);
  }).catch(() => {
    // SDK load failed — abort silently. This is non-critical.
    return;
  });

  if (typeof window.funnelbarn?.init !== "function") return;

  window.funnelbarn.init({ apiKey, endpoint });
  window.funnelbarn.page();

  // Track subsequent hash-based route changes as additional page views.
  window.addEventListener("hashchange", () => {
    window.funnelbarn?.page();
  });
}

// ---------------------------------------------------------------------------
// Self-reporting — dogfood BugBarn by capturing frontend errors into itself.
// ---------------------------------------------------------------------------

let selfReportApiKey = "";
let selfReportProject = "";

function sendErrorEnvelope(error: unknown): void {
  if (!selfReportApiKey) return;
  const err = error instanceof Error ? error : new Error(String(error));
  const headers: Record<string, string> = {
    "content-type": "application/json",
    "x-bugbarn-api-key": selfReportApiKey,
  };
  if (selfReportProject) {
    headers["x-bugbarn-project"] = selfReportProject;
  }
  const body = JSON.stringify({
    timestamp: new Date().toISOString(),
    severityText: "ERROR",
    body: err.message,
    exception: {
      type: err.name || "Error",
      message: err.message,
      stacktrace: parseStack(err.stack),
    },
    attributes: {
      url: location.href,
      userAgent: navigator.userAgent,
    },
    sender: { sdk: { name: "bugbarn.web", version: "0.1.0" } },
  });
  fetch(apiUrl("/api/v1/events"), { method: "POST", headers, body, keepalive: true }).catch(() => {});
}

function parseStack(stack?: string): Array<{ file: string; line: number; column: number; function?: string }> | undefined {
  if (!stack) return undefined;
  const frames: Array<{ file: string; line: number; column: number; function?: string }> = [];
  for (const raw of stack.split("\n").map((l) => l.trim()).slice(1)) {
    const m = /^at (?:(.+?) )?\(?(.+?):(\d+):(\d+)\)?$/.exec(raw);
    if (!m) continue;
    const f: { file: string; line: number; column: number; function?: string } = {
      file: m[2],
      line: Number(m[3]),
      column: Number(m[4]),
    };
    if (m[1]) f.function = m[1];
    frames.push(f);
  }
  return frames.length > 0 ? frames : undefined;
}

// Populates the IAMBarn profile + sign-out links on the Settings → Session
// card. Mobile users have no sidebar/bb-menu, so this is their only path to
// log out or hop to their iambarn profile.
async function initSettingsIAMBarnLinks(): Promise<void> {
  if (!document.cookie.split("; ").some((c) => c.startsWith("bugbarn_auth_method=oidc"))) {
    return;
  }
  let cfg: { iambarn?: { profileURL?: string }; oidc?: OIDCRuntime } = {};
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return;
    cfg = await res.json() as typeof cfg;
  } catch {
    return;
  }
  const profile = elements.overviewView.querySelector<HTMLAnchorElement>("#settings-iambarn-profile");
  if (profile && cfg.iambarn?.profileURL) {
    profile.href = cfg.iambarn.profileURL;
    profile.removeAttribute("hidden");
  }
  const signOut = elements.overviewView.querySelector<HTMLAnchorElement>("#settings-iambarn-logout");
  if (signOut && cfg.oidc?.endSessionURL) {
    const ret = `${window.location.origin}/`;
    const sep = cfg.oidc.endSessionURL.includes("?") ? "&" : "?";
    signOut.href = `${cfg.oidc.endSessionURL}${sep}post_logout_redirect_uri=${encodeURIComponent(ret)}`;
    signOut.removeAttribute("hidden");
  }
}

async function initIAMBarnProfileLink(): Promise<void> {
  // Only show the IAMBarn profile link when the current session was
  // actually established via the iambarn OIDC callback — local
  // single-user installs shouldn't be linking to a remote profile
  // they don't have.
  if (!document.cookie.split("; ").some((c) => c.startsWith("bugbarn_auth_method=oidc"))) {
    return;
  }
  let cfg: { iambarn?: { profileURL?: string } };
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return;
    cfg = await res.json() as typeof cfg;
  } catch {
    return;
  }
  const url = cfg?.iambarn?.profileURL;
  if (!url) return;
  const link = document.getElementById("bb-iambarn-profile") as HTMLAnchorElement | null;
  if (!link) return;
  link.href = url;
  link.removeAttribute("hidden");
}

async function initSelfReporting(): Promise<void> {
  let cfg: { bugbarn?: { enabled: boolean; apiKey?: string; project?: string } };
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return;
    cfg = await res.json() as typeof cfg;
  } catch {
    return;
  }

  const bb = cfg?.bugbarn;
  if (!bb?.enabled || !bb.apiKey) return;

  selfReportApiKey = bb.apiKey;
  selfReportProject = bb.project ?? "";

  window.addEventListener("error", (ev) => {
    if (ev.error) sendErrorEnvelope(ev.error);
  });
  window.addEventListener("unhandledrejection", (ev) => {
    sendErrorEnvelope(ev.reason);
  });
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

function showFlash(message: string, tone: "error" | "success" | "info" = "info", durationMs = 5000): void {
  const existing = document.getElementById("flash-banner");
  if (existing) existing.remove();

  const banner = document.createElement("div");
  banner.id = "flash-banner";
  banner.setAttribute("role", "alert");
  const bg = tone === "error" ? "#5c1a1a" : tone === "success" ? "#1a3d1a" : "#1a2a3d";
  const border = tone === "error" ? "#a33" : tone === "success" ? "#3a3" : "#369";
  banner.style.cssText = `position:fixed;top:0;left:0;right:0;z-index:10000;padding:10px 16px;background:${bg};border-bottom:2px solid ${border};color:#eee;font-size:0.85rem;display:flex;align-items:center;justify-content:space-between;gap:8px;animation:flashIn 0.2s ease-out`;
  banner.innerHTML = `<span>${escapeHtml(message)}</span><button style="background:none;border:none;color:#aaa;cursor:pointer;font-size:1.1rem;padding:0 4px" aria-label="Dismiss">&times;</button>`;
  document.body.prepend(banner);

  banner.querySelector("button")?.addEventListener("click", () => banner.remove());
  if (durationMs > 0) {
    setTimeout(() => banner.remove(), durationMs);
  }
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
    const validTabs: SettingsTab[] = ["overview", "projects", "preferences", "keys"];
    state.settingsTab = (validTabs.includes(id as SettingsTab) ? id : "overview") as SettingsTab;
    const subPageTitles: Record<string, string> = { projects: "Projects", preferences: "Preferences", keys: "API Keys" };
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

function setRouteStatus(): void {
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
    const tasks: Promise<void>[] = [loadIssues(), loadCurrentRouteData()];
    if (!state.selectedIssueId && !state.selectedEventId) {
      tasks.push(loadLiveEvents());
    }
    await Promise.all(tasks);
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

let issueLoadGeneration = 0;

const ISSUE_PAGE_SIZE = 50;

async function loadIssues(): Promise<void> {
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

async function loadMoreIssues(): Promise<void> {
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

async function loadSparklines(): Promise<void> {
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

async function loadReleases(): Promise<void> {
  try {
    const payload = await fetchJson("/api/v1/releases", true);
    state.releases = payload ? normalizeList<ApiRelease>(payload, "releases") : [];
    if (state.currentRoute === "releases") renderReleasesView();
  } catch (error) {
    state.releases = [];
    if (state.currentRoute === "releases") renderReleasesView(error);
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
    if (state.currentRoute === "alerts") renderAlertsView();
  } catch (error) {
    state.alerts = [];
    if (state.currentRoute === "alerts") renderAlertsView(error);
  }
}

async function loadSettings(): Promise<void> {
  try {
    const [settingsPayload, keysPayload, projectsPayload, groupsPayload, aliasesPayload] = await Promise.all([
      fetchJson("/api/v1/settings", true),
      fetchJson("/api/v1/apikeys", true).catch(() => null),
      fetchJson("/api/v1/projects", true).catch(() => null),
      fetchJson("/api/v1/groups", true).catch(() => null),
      fetchJson("/api/v1/aliases", true).catch(() => null),
    ]);
    state.settings = settingsPayload ? normalizeObject<ApiSettings>(settingsPayload, "settings") : null;
    state.apiKeys = keysPayload ? normalizeList<ApiApiKey>(keysPayload as Record<string, unknown>, "apiKeys") : [];
    if (projectsPayload) state.projects = normalizeList<ApiProject>(projectsPayload, "projects");
    state.groups = groupsPayload ? ((groupsPayload as Record<string, unknown>)["groups"] as ApiProjectGroup[] ?? []) : [];
    state.aliases = aliasesPayload ? ((aliasesPayload as Record<string, unknown>)["aliases"] as ApiAlias[] ?? []) : [];
    renderProjectSwitcher();
    if (state.currentRoute === "settings") renderSettingsView();
  } catch (error) {
    state.settings = null;
    if (state.currentRoute === "settings") renderSettingsView(error);
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
  renderProjectPicker();
  updateScopeBtn();
}

function renderProjectSwitcher(): void {
  renderProjectPicker();
  updateScopeBtn();
}

function openPicker(): void {
  if (!pickerDropdown || !pickerFilter) return;
  pickerDropdown.hidden = false;
  pickerFilter.value = "";
  renderPickerList("");
  pickerFilter.focus();
}

function closePicker(): void {
  if (pickerDropdown) pickerDropdown.hidden = true;
}

function renderProjectPicker(): void {
  updatePickerBtn();
  if (pickerDropdown && !pickerDropdown.hidden) renderPickerList(pickerFilter?.value ?? "");
}

function updatePickerBtn(): void {
  if (!pickerBtn) return;
  let label: string;
  if (state.currentGroup) {
    const g = state.groups.find(g => g.slug === state.currentGroup);
    label = g ? g.name : state.currentGroup;
  } else {
    const proj = state.projects.find(p => String(p.slug ?? p.Slug ?? "") === state.currentProject);
    label = proj ? String(proj.name ?? proj.Name ?? state.currentProject) : "All projects";
  }
  if (state.currentEnv) label += ` / ${state.currentEnv}`;
  pickerBtn.innerHTML = `${escapeHtml(label)} <span class="scope-chevron">▾</span>`;
}

function renderPickerList(filter: string): void {
  if (!pickerList) return;
  const f = filter.trim().toLowerCase();

  const matchProject = (p: ApiProject) => {
    if (!f) return true;
    return String(p.name ?? p.Name ?? "").toLowerCase().includes(f) ||
           String(p.slug ?? p.Slug ?? "").toLowerCase().includes(f);
  };
  const matchGroup = (g: ApiProjectGroup) => !f ||
    g.name.toLowerCase().includes(f) || g.slug.toLowerCase().includes(f) ||
    state.projects.some(p => p.group_id === g.id && matchProject(p));

  let html = "";

  const allSel = !state.currentGroup && (!state.currentProject || state.currentProject === "__all");
  if (!f || "all projects".includes(f)) {
    html += `<button class="picker-item${allSel ? " selected" : ""}" data-pick-project="__all">All projects</button>`;
  }

  const visibleGroups = state.groups.filter(matchGroup);
  if (visibleGroups.length > 0) {
    html += `<div class="picker-section-label">Groups</div>`;
    for (const g of visibleGroups) {
      const sel = state.currentGroup === g.slug;
      const count = state.projects.filter(p => p.group_id === g.id).length;
      html += `<button class="picker-item${sel ? " selected" : ""}" data-pick-group="${escapeHtml(g.slug)}">${escapeHtml(g.name)}<span class="picker-item-badge">${count}</span></button>`;
    }
  }

  const visibleProjects = state.projects.filter(matchProject);
  if (visibleProjects.length > 0) {
    html += `<div class="picker-section-label">Projects</div>`;
    for (const p of visibleProjects) {
      const slug = String(p.slug ?? p.Slug ?? "");
      const name = String(p.name ?? p.Name ?? slug);
      const sel = !state.currentGroup && slug === state.currentProject;
      html += `<button class="picker-item${sel ? " selected" : ""}" data-pick-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
    }
  }

  if (!html) html = `<p class="picker-empty">No matches</p>`;
  pickerList.innerHTML = html;

  pickerList.querySelectorAll<HTMLButtonElement>("[data-pick-group]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["pickGroup"] ?? "";
      state.currentGroup = slug; localStorage.setItem(groupKey, slug);
      state.currentProject = "__all"; localStorage.removeItem(projectKey);
      state.currentEnv = ""; localStorage.removeItem(envKey);
      renderEnvSwitcher([]); updatePickerBtn(); updateScopeBtn(); closePicker();
      void refreshAll();
    });
  });

  pickerList.querySelectorAll<HTMLButtonElement>("[data-pick-project]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["pickProject"] ?? "__all";
      state.currentGroup = null; localStorage.removeItem(groupKey);
      state.currentProject = slug; localStorage.setItem(projectKey, slug);
      state.currentEnv = ""; localStorage.removeItem(envKey);
      updatePickerBtn(); updateScopeBtn(); closePicker();
      if (slug === "__all") { renderEnvSwitcher([]); void refreshAll(); }
      else { void Promise.all([loadEnvironments(), refreshAll()]); }
    });
  });
}

function updateScopeBtn(): void {
  const btn = document.getElementById("scope-btn");
  if (!btn) return;
  // Reuse the same label logic as the desktop picker
  updatePickerBtn();
  let label: string;
  if (state.currentGroup) {
    const g = state.groups.find(g => g.slug === state.currentGroup);
    label = g ? g.name : state.currentGroup;
  } else {
    const proj = state.projects.find(p => String(p.slug ?? p.Slug ?? "") === state.currentProject);
    label = proj ? String(proj.name ?? proj.Name ?? state.currentProject) : "All projects";
  }
  if (state.currentEnv) label += ` / ${state.currentEnv}`;
  btn.innerHTML = `${escapeHtml(label)} <span class="scope-chevron">▾</span>`;
}

async function checkPendingProjects(): Promise<void> {
  const banner = document.getElementById("pending-banner");
  if (!banner) return;
  try {
    const res = await fetch(apiUrl("/api/v1/projects/pending-count"), {
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) return;
    const data = (await res.json()) as { count: number; slugs: string[] };
    if (data.count === 0) {
      banner.hidden = true;
      updatePendingBadge(0);
      return;
    }
    const slugList = (data.slugs ?? []).map((s: string) => escapeHtml(s)).join(", ");
    banner.innerHTML =
      `<span>${data.count} project${data.count > 1 ? "s" : ""} awaiting approval: <strong>${slugList}</strong></span>` +
      `<a href="#/settings" class="pending-banner-link">Review in Settings</a>`;
    banner.hidden = false;
    updatePendingBadge(data.count);
  } catch {
    // Silently ignore — non-critical.
  }
}

function updatePendingBadge(count: number): void {
  const settingsLink = document.querySelector<HTMLAnchorElement>('.side-nav a[data-route="settings"]');
  if (!settingsLink) return;
  let badge = settingsLink.querySelector<HTMLElement>(".nav-badge");
  if (count === 0) {
    badge?.remove();
    return;
  }
  if (!badge) {
    badge = document.createElement("span");
    badge.className = "nav-badge";
    settingsLink.appendChild(badge);
  }
  badge.textContent = String(count);
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
  updateScopeBtn();
}

// ── Scope picker (mobile bottom sheet) ──────────────────────────────────
const scopeBtn = document.getElementById("scope-btn");
const scopeSheet = document.getElementById("scope-sheet");
const scopeBackdrop = document.getElementById("scope-backdrop");
const scopeClose = document.getElementById("scope-close");
const scopeBody = document.getElementById("scope-sheet-body");

function openScopeSheet(): void {
  if (!scopeSheet || !scopeBackdrop || !scopeBody) return;
  renderScopeSheetBody();
  scopeSheet.hidden = false;
  scopeBackdrop.hidden = false;
}

function closeScopeSheet(): void {
  if (scopeSheet) scopeSheet.hidden = true;
  if (scopeBackdrop) scopeBackdrop.hidden = true;
}

function renderScopeSheetBody(filter = ""): void {
  if (!scopeBody) return;
  const f = filter.trim().toLowerCase();
  const current = state.currentProject;
  let html = `<input class="scope-sheet-filter" type="text" placeholder="Filter…" value="${escapeHtml(filter)}" autocomplete="off" />`;

  const matchesFilter = (name: string, slug: string) => !f || name.toLowerCase().includes(f) || slug.toLowerCase().includes(f);

  if (state.groups.length > 0) {
    const visGroups = state.groups.filter(g => matchesFilter(g.name, g.slug));
    if (visGroups.length > 0 || !f) {
      html += `<div class="scope-section-label">Group</div>`;
      if (!f) html += `<button class="scope-item${state.currentGroup === null && (current === "__all" || !current) ? " selected" : ""}" data-scope-project="__all">All projects</button>`;
      for (const g of visGroups) {
        const selected = state.currentGroup === g.slug;
        html += `<button class="scope-item${selected ? " selected" : ""}" data-scope-group="${escapeHtml(g.slug)}">${escapeHtml(g.name)}</button>`;
      }
      html += `<div class="scope-divider"></div>`;
    }
    html += `<div class="scope-section-label">Project</div>`;
    if (f && "all projects".includes(f)) html += `<button class="scope-item" data-scope-project="__all">All projects</button>`;
    for (const p of state.projects) {
      const slug = String(p.slug ?? p.Slug ?? "");
      const name = String(p.name ?? p.Name ?? slug);
      if (!matchesFilter(name, slug)) continue;
      const selected = !state.currentGroup && slug === current;
      html += `<button class="scope-item${selected ? " selected" : ""}" data-scope-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
    }
  } else {
    html += `<div class="scope-section-label">Project</div>`;
    if (!f || "all projects".includes(f)) html += `<button class="scope-item${current === "__all" || !current ? " selected" : ""}" data-scope-project="__all">All projects</button>`;
    for (const p of state.projects) {
      const slug = String(p.slug ?? p.Slug ?? "");
      const name = String(p.name ?? p.Name ?? slug);
      if (!matchesFilter(name, slug)) continue;
      html += `<button class="scope-item${slug === current ? " selected" : ""}" data-scope-project="${escapeHtml(slug)}">${escapeHtml(name)}</button>`;
    }
  }

  if (!state.currentGroup && current && current !== "__all") {
    html += `<div class="scope-divider"></div>`;
    html += `<div class="scope-section-label">Environment</div>`;
    html += `<button class="scope-item scope-item-sub${!state.currentEnv ? " selected" : ""}" data-scope-env="">All environments</button>`;
    html += `<div id="scope-env-list"></div>`;
    loadScopeEnvs();
  }
  scopeBody.innerHTML = html;

  scopeBody.querySelector<HTMLInputElement>(".scope-sheet-filter")?.addEventListener("input", (e) => {
    renderScopeSheetBody((e.target as HTMLInputElement).value);
  });

  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-group]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.getAttribute("data-scope-group") ?? "";
      state.currentGroup = slug;
      localStorage.setItem(groupKey, slug);
      state.currentProject = "__all";
      localStorage.removeItem(projectKey);
      state.currentEnv = "";
      localStorage.removeItem(envKey);
      renderEnvSwitcher([]);
      renderProjectSwitcher();
      closeScopeSheet();
      void refreshAll();
    });
  });

  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-project]").forEach(btn => {
    btn.addEventListener("click", () => {
      const slug = btn.getAttribute("data-scope-project") ?? "__all";
      state.currentGroup = null;
      localStorage.removeItem(groupKey);
      state.currentProject = slug;
      localStorage.setItem(projectKey, slug);
      state.currentEnv = "";
      localStorage.removeItem(envKey);
      renderProjectSwitcher();
      if (slug === "__all") {
        renderEnvSwitcher([]);
        closeScopeSheet();
        void refreshAll();
      } else {
        renderScopeSheetBody();
        void Promise.all([loadEnvironments(), refreshAll()]);
      }
    });
  });
  scopeBody.querySelectorAll<HTMLButtonElement>("[data-scope-env]").forEach(btn => {
    btn.addEventListener("click", () => {
      const env = btn.getAttribute("data-scope-env") ?? "";
      state.currentEnv = env;
      if (env) localStorage.setItem(envKey, env); else localStorage.removeItem(envKey);
      renderEnvSwitcher([]);
      updateScopeBtn();
      closeScopeSheet();
      void refreshAll();
    });
  });
}

async function loadScopeEnvs(): Promise<void> {
  const el = document.getElementById("scope-env-list");
  if (!el) return;
  try {
    const payload = await fetchJson("/api/v1/facets/attributes.environment", true);
    const raw = payload as Record<string, unknown>;
    const envs = Array.isArray(raw?.["values"]) ? (raw["values"] as string[]) : [];
    let html = "";
    for (const e of envs) {
      html += `<button class="scope-item scope-item-sub${e === state.currentEnv ? " selected" : ""}" data-scope-env="${escapeHtml(e)}">${escapeHtml(e)}</button>`;
    }
    el.innerHTML = html;
    el.querySelectorAll<HTMLButtonElement>("[data-scope-env]").forEach(btn => {
      btn.addEventListener("click", () => {
        const env = btn.getAttribute("data-scope-env") ?? "";
        state.currentEnv = env;
        if (env) localStorage.setItem(envKey, env); else localStorage.removeItem(envKey);
        updateScopeBtn();
        closeScopeSheet();
        void refreshAll();
      });
    });
  } catch {
    el.innerHTML = "";
  }
}

scopeBtn?.addEventListener("click", openScopeSheet);
scopeClose?.addEventListener("click", closeScopeSheet);
scopeBackdrop?.addEventListener("click", closeScopeSheet);

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
      notifyRegression(event);
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

function _stopLivePolling(): void {
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

const notifiedRegressionKey = "bugbarn_notified_regressions";

function notifyRegression(event: ApiEvent): void {
  const regressed = (event as Record<string, unknown>)["Regressed"] ?? (event as Record<string, unknown>)["regressed"];
  if (!regressed) return;
  if (!("Notification" in window) || Notification.permission !== "granted") return;

  const issueId = String(event.IssueID ?? event.issueId ?? event.issue_id ?? "");
  if (!issueId) return;

  const notified: string[] = JSON.parse(localStorage.getItem(notifiedRegressionKey) || "[]");
  if (notified.includes(issueId)) return;

  notified.push(issueId);
  if (notified.length > 200) notified.splice(0, notified.length - 200);
  localStorage.setItem(notifiedRegressionKey, JSON.stringify(notified));

  const title = String(event.Message ?? event.message ?? event.Title ?? event.title ?? "Issue regressed");
  if ("serviceWorker" in navigator && navigator.serviceWorker.controller) {
    navigator.serviceWorker.ready.then((reg) => {
      reg.showNotification("BugBarn: Issue Regressed", {
        body: title,
        icon: "/icons/icon-192.png",
        tag: `regressed-${issueId}`,
        data: { url: `${location.origin}/#/issues/${encodeURIComponent(issueId)}` },
      });
    });
  } else {
    new Notification("BugBarn: Issue Regressed", {
      body: title,
      icon: "/icons/icon-192.png",
      tag: `regressed-${issueId}`,
    });
  }
}

function requestNotificationPermission(): void {
  if (!("Notification" in window)) return;
  if (Notification.permission !== "default") return;
  Notification.requestPermission();
}

async function fetchJson(path: string, allowMissing = false): Promise<unknown> {
  const url = apiUrl(path);
  const existing = state.inFlight.get(url);
  if (existing) {
    return existing;
  }

  const headers: Record<string, string> = { Accept: "application/json" };
  if (state.currentGroup) {
    headers["X-BugBarn-Group"] = state.currentGroup;
  } else if (state.currentProject && state.currentProject !== "default" && state.currentProject !== "__all") {
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

async function refreshCSRFToken(): Promise<void> {
  await fetch(apiUrl("/api/v1/me"), { credentials: "include", headers: { Accept: "application/json" } });
}

async function postJson(path: string, body: unknown, _retried = false): Promise<unknown> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  };
  if (csrf) {
    headers["X-BugBarn-CSRF"] = csrf;
  }
  if (state.currentGroup) {
    headers["X-BugBarn-Group"] = state.currentGroup;
  } else if (state.currentProject && state.currentProject !== "default" && state.currentProject !== "__all") {
    headers["X-BugBarn-Project"] = state.currentProject;
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
  if (response.status === 403 && !_retried) {
    const text = await response.text().catch(() => "");
    if (text.includes("CSRF")) {
      await refreshCSRFToken();
      return postJson(path, body, true);
    }
    throw new Error(`${response.status} ${response.statusText}: ${text.slice(0, 200).trim()}`.trim());
  }
  if (!response.ok) {
    const body = await response.text().catch(() => "");
    const detail = body ? `: ${body.slice(0, 200).trim()}` : "";
    throw new Error(`${response.status} ${response.statusText}${detail}`.trim());
  }
  const text = await response.text();
  return text ? JSON.parse(text) as unknown : null;
}

async function deleteJson(path: string): Promise<unknown> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = { Accept: "application/json" };
  if (csrf) headers["X-BugBarn-CSRF"] = csrf;
  const response = await fetch(apiUrl(path), { method: "DELETE", credentials: "include", headers });
  if (!response.ok) {
    const body = await response.text().catch(() => "");
    const detail = body ? `: ${body.slice(0, 200).trim()}` : "";
    throw new Error(`${response.status} ${response.statusText}${detail}`.trim());
  }
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
  elements.overviewView.innerHTML = renderSettingsViewMarkup(state.settings, state.username, state.apiKeys, error, state.projects, state.groups, state.aliases, state.settingsTab);
  wireSettingsActions();
}

function _renderDetail(): void {
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
  // If the OIDC callback bounced us here with ?oidc_error=…, show an inline
  // error card with switch/sign-out actions instead of silently looping the
  // user back into the IdP (which would just re-reject the same identity).
  const oidcErr = readOIDCErrorFromURL();
  if (oidcErr) {
    void renderOIDCAccessDenied(oidcErr);
  } else {
    void maybeRedirectToOIDC();
  }
  if (loginScreen) loginScreen.hidden = false;
  if (appFrame) appFrame.hidden = true;
  if (loginError) {
    loginError.hidden = !error;
    loginError.textContent = error;
  }
  (loginForm?.querySelector('input[name="username"]') as HTMLInputElement | null)?.focus();
}

type OIDCRuntime = {
  enabled?: boolean;
  loginURL?: string;
  switchAccountURL?: string;
  endSessionURL?: string;
};

function readOIDCErrorFromURL(): { code: string; identity: string } | null {
  const params = new URLSearchParams(window.location.search);
  const code = params.get("oidc_error");
  if (!code) return null;
  return { code, identity: params.get("identity") ?? "" };
}

async function renderOIDCAccessDenied(err: { code: string; identity: string }): Promise<void> {
  let oc: OIDCRuntime | undefined;
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (res.ok) {
      const cfg = await res.json() as { oidc?: OIDCRuntime };
      oc = cfg?.oidc;
    }
  } catch {
    // Best-effort — we still render a useful message even without runtime config.
  }
  if (loginError) {
    const who = err.identity ? ` as ${err.identity}` : "";
    loginError.hidden = false;
    loginError.textContent = "";
    const msg = document.createElement("div");
    msg.textContent = `You're signed in to IAMBarn${who}, but that account doesn't have access to BugBarn.`;
    loginError.appendChild(msg);
    const actions = document.createElement("div");
    actions.className = "login-error-actions";
    if (oc?.switchAccountURL ?? oc?.loginURL) {
      const a = document.createElement("a");
      a.href = oc.switchAccountURL ?? oc.loginURL!;
      a.textContent = "Switch account";
      actions.appendChild(a);
    }
    if (oc?.endSessionURL) {
      const a = document.createElement("a");
      const ret = `${window.location.origin}/`;
      const sep = oc.endSessionURL.includes("?") ? "&" : "?";
      a.href = `${oc.endSessionURL}${sep}post_logout_redirect_uri=${encodeURIComponent(ret)}`;
      a.textContent = "Sign out of IAMBarn";
      actions.appendChild(a);
    }
    if (actions.children.length) loginError.appendChild(actions);
  }
  // Strip the query string so a reload doesn't re-show the error (and doesn't
  // leak the identity into browser history beyond this view).
  history.replaceState(null, "", window.location.pathname + window.location.hash);
}

let oidcRedirectStarted = false;
async function maybeRedirectToOIDC(): Promise<void> {
  if (oidcRedirectStarted) return;
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return;
    const cfg = await res.json() as { oidc?: OIDCRuntime };
    const oc = cfg?.oidc;
    if (oc?.enabled && oc.loginURL) {
      oidcRedirectStarted = true;
      window.location.assign(oc.loginURL);
    }
  } catch {
    // Network error — fall back to the local login form silently.
  }
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
    if (loginScreen) loginScreen.hidden = true;
    if (appFrame) appFrame.hidden = false;
    updateBBMenuUser();
    setStatus(state.username ? `Logged in as ${state.username}.` : "Logged in.");
    await Promise.all([loadProjects(), refreshAll()]);
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
  elements.detailTitle.textContent = issueId ? `${issueId} — ${firstIdentifier(event)}` : firstIdentifier(event) || eventTitle(event);
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

  const copySetupBtn = elements.overviewView.querySelector<HTMLButtonElement>("#copy-setup-url");
  copySetupBtn?.addEventListener("click", () => {
    const urlEl = elements.overviewView.querySelector<HTMLElement>("#setup-url");
    if (urlEl) {
      void navigator.clipboard.writeText(urlEl.textContent || "");
      copySetupBtn.textContent = "✓";
      setTimeout(() => { copySetupBtn.textContent = "⧉"; }, 1500);
    }
  });

  const logoutBtn = elements.overviewView.querySelector<HTMLButtonElement>("#settings-logout");
  logoutBtn?.addEventListener("click", () => { void logout(); });
  void initSettingsIAMBarnLinks();

  wireProjectListControls();

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-approve-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["approveProject"];
      if (slug) void approveProject(slug);
    });
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteProject"];
      if (slug) void deleteProject(slug);
    });
  });

  // Group actions
  const createGroupForm = elements.overviewView.querySelector<HTMLFormElement>("#create-group-form");
  createGroupForm?.addEventListener("submit", (e) => {
    e.preventDefault();
    const data = new FormData(createGroupForm);
    const name = String(data.get("name") || "").trim();
    if (name) void createGroup(name);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-group]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteGroup"];
      if (slug) void deleteGroup(slug);
    });
  });

  elements.overviewView.querySelectorAll<HTMLFormElement>("[data-add-to-group]").forEach((form) => {
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const groupSlug = form.dataset["addToGroup"]!;
      const select = form.querySelector<HTMLSelectElement>("select");
      const projectSlug = select?.value;
      if (projectSlug) void addProjectToGroup(groupSlug, projectSlug);
    });
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-remove-from-group]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const projectSlug = btn.dataset["removeFromGroup"];
      if (projectSlug) void removeProjectFromGroup(projectSlug);
    });
  });

  // Alias actions
  const createAliasForm = elements.overviewView.querySelector<HTMLFormElement>("#create-alias-form");
  createAliasForm?.addEventListener("submit", (e) => {
    e.preventDefault();
    const data = new FormData(createAliasForm);
    const alias = String(data.get("alias") || "").trim();
    const project = String(data.get("project") || "").trim();
    if (alias && project) void createAlias(alias, project);
  });

  elements.overviewView.querySelectorAll<HTMLButtonElement>("[data-delete-alias]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const slug = btn.dataset["deleteAlias"];
      if (slug) void deleteAlias(slug);
    });
  });
}

function wireProjectListControls(): void {
  const list = elements.overviewView.querySelector<HTMLElement>("#project-list");
  const filter = elements.overviewView.querySelector<HTMLInputElement>("#project-filter");
  const sort = elements.overviewView.querySelector<HTMLSelectElement>("#project-sort");
  const statusFilter = elements.overviewView.querySelector<HTMLSelectElement>("#project-status-filter");
  if (!list) return;

  const rows = Array.from(list.querySelectorAll<HTMLElement>(".project-row"));

  const apply = (): void => {
    const q = (filter?.value ?? "").trim().toLowerCase();
    const statusVal = statusFilter?.value ?? "all";
    const sortVal = sort?.value ?? "default";

    const visible = rows.filter((row) => {
      const name = row.dataset["name"] ?? "";
      const slug = (row.dataset["slug"] ?? "").toLowerCase();
      const status = row.dataset["status"] ?? "";
      if (q && !name.includes(q) && !slug.includes(q)) return false;
      if (statusVal !== "all" && status !== statusVal) return false;
      return true;
    });

    rows.forEach((row) => { row.hidden = !visible.includes(row); });

    const num = (row: HTMLElement, key: string): number => Number(row.dataset[key] ?? 0);
    const byName = (a: HTMLElement, b: HTMLElement, dir: 1 | -1): number =>
      (a.dataset["name"] ?? "").localeCompare(b.dataset["name"] ?? "") * dir;

    let sorted = [...visible];
    switch (sortVal) {
      case "name-asc": sorted.sort((a, b) => byName(a, b, 1)); break;
      case "name-desc": sorted.sort((a, b) => byName(a, b, -1)); break;
      case "issues-desc": sorted.sort((a, b) => num(b, "issues") - num(a, "issues")); break;
      case "events-desc": sorted.sort((a, b) => num(b, "events") - num(a, "events")); break;
      case "logs-desc": sorted.sort((a, b) => num(b, "logs") - num(a, "logs")); break;
      case "status": sorted.sort((a, b) => (a.dataset["status"] ?? "").localeCompare(b.dataset["status"] ?? "") || byName(a, b, 1)); break;
      default:
        sorted.sort((a, b) => {
          const ap = a.dataset["status"] === "pending" ? 0 : 1;
          const bp = b.dataset["status"] === "pending" ? 0 : 1;
          return ap - bp || byName(a, b, 1);
        });
    }
    sorted.forEach((row) => { list.appendChild(row); });
  };

  filter?.addEventListener("input", apply);
  sort?.addEventListener("change", apply);
  statusFilter?.addEventListener("change", apply);
}

async function approveProject(slug: string): Promise<void> {
  try {
    await postJson(`/api/v1/projects/${encodeURIComponent(slug)}/approve`, {});
    showFlash(`Project "${slug}" approved.`, "success");
    await loadSettings();
    void checkPendingProjects();
  } catch (error) {
    showFlash(`Approve failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteProject(slug: string): Promise<void> {
  if (!confirm(`Delete project "${slug}"? This will remove all its issues and events.`)) return;
  try {
    const res = await apiFetch(`/api/v1/projects/${encodeURIComponent(slug)}`, { method: "DELETE" });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as Record<string, string>).error || res.statusText);
    }
    showFlash(`Project "${slug}" deleted.`, "success");
    await loadSettings();
    void checkPendingProjects();
  } catch (error) {
    showFlash(`Delete failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function createGroup(name: string): Promise<void> {
  try {
    await postJson("/api/v1/groups", { name });
    showFlash(`Group "${name}" created.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Create group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteGroup(slug: string): Promise<void> {
  if (!confirm(`Delete group "${slug}"? Projects will be ungrouped but not deleted.`)) return;
  try {
    const res = await apiFetch(`/api/v1/groups/${encodeURIComponent(slug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`Group "${slug}" deleted.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Delete group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function addProjectToGroup(groupSlug: string, projectSlug: string): Promise<void> {
  try {
    await postJson(`/api/v1/groups/${encodeURIComponent(groupSlug)}/projects`, { project: projectSlug });
    showFlash(`"${projectSlug}" added to group.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Add to group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function removeProjectFromGroup(projectSlug: string): Promise<void> {
  try {
    const group = state.groups.find(g => state.projects.find(p => (p.slug ?? p.Slug) === projectSlug && p.group_id === g.id));
    if (!group) throw new Error("project not in a group");
    const res = await apiFetch(`/api/v1/groups/${encodeURIComponent(group.slug)}/projects/${encodeURIComponent(projectSlug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`"${projectSlug}" removed from group.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Remove from group failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function createAlias(alias: string, project: string): Promise<void> {
  try {
    await postJson("/api/v1/aliases", { alias, project });
    showFlash(`Alias "${alias}" → "${project}" created.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Create alias failed: ${errorMessage(error)}`, "error", 8000);
  }
}

async function deleteAlias(aliasSlug: string): Promise<void> {
  try {
    const res = await apiFetch(`/api/v1/aliases/${encodeURIComponent(aliasSlug)}`, { method: "DELETE" });
    if (!res.ok) throw new Error(res.statusText);
    showFlash(`Alias "${aliasSlug}" deleted.`, "success");
    await loadSettings();
  } catch (error) {
    showFlash(`Delete alias failed: ${errorMessage(error)}`, "error", 8000);
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

async function apiFetch(path: string, init: RequestInit = {}, _retried = false): Promise<Response> {
  const csrf = getCSRFToken();
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
    ...(init.headers as Record<string, string> ?? {}),
  };
  if (csrf) {
    headers["X-BugBarn-CSRF"] = csrf;
  }
  if (state.currentGroup) {
    headers["X-BugBarn-Group"] = state.currentGroup;
  } else if (state.currentProject && state.currentProject !== "default" && state.currentProject !== "__all") {
    headers["X-BugBarn-Project"] = state.currentProject;
  }
  const res = await fetch(apiUrl(path), { credentials: "include", ...init, headers });
  if (res.status === 403 && !_retried) {
    const text = await res.clone().text().catch(() => "");
    if (text.includes("CSRF")) {
      await refreshCSRFToken();
      return apiFetch(path, init, true);
    }
  }
  return res;
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
      showFlash(`Mute failed: ${res.status} ${res.statusText}`.trim(), "error");
    }
  } catch (error) {
    showFlash(`Mute failed: ${errorMessage(error)}`, "error");
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
      showFlash(`Unmute failed: ${res.status} ${res.statusText}`.trim(), "error");
    }
  } catch (error) {
    showFlash(`Unmute failed: ${errorMessage(error)}`, "error");
  }
}

async function toggleIssueStatus(issueId: string, status: "resolved" | "unresolved"): Promise<void> {
  try {
    await postJson(`/api/v1/issues/${encodeURIComponent(issueId)}/${status === "resolved" ? "resolve" : "reopen"}`, {});
    showFlash(`Issue ${issueId} marked ${status}.`, "success");
    await loadIssueDetail(issueId);
    await loadIssues();
  } catch (error) {
    showFlash(`Issue status change failed: ${errorMessage(error)}`, "error");
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
    if (state.currentRoute === "logs") renderLogsView();
  } catch {
    state.logs = [];
    if (state.currentRoute === "logs") renderLogsView();
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

function setActiveView(view: "overview" | "detail"): void {
  elements.overviewView.classList.toggle("hidden", view !== "overview");
  elements.detailView.classList.toggle("hidden", view !== "detail");
}

const logLevelNums: Record<string, number> = { trace: 10, debug: 20, info: 30, warn: 40, error: 50, fatal: 60 };

function logLevelMinNum(levelName: string): number {
  return logLevelNums[levelName] ?? 0;
}

function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}






