// Shared application core: the mutable singletons (`state`, `elements`), the
// cached static DOM references, app-wide constants, and the leaf DOM/util
// helpers that every feature module depends on. Splitting these out lets the
// per-feature modules (http, router, views, bootstrap) import a single,
// dependency-free core without import cycles.
import { escapeHtml } from "./format.js";
import type { AppElements, AppState } from "./types.js";

export const httpUnauthorized = 401;
export const liveWindowMinutes = 15;

export const sidebarKey = "bugbarn_sidebar";
export const projectKey = "bugbarn_project";
export const envKey = "bugbarn_env";
export const groupKey = "bugbarn_group";
export const notifiedRegressionKey = "bugbarn_notified_regressions";

export type OIDCRuntime = {
  enabled?: boolean;
  loginURL?: string;
  switchAccountURL?: string;
  endSessionURL?: string;
};

export function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element: ${id}`);
  }
  return element as T;
}

export const state: AppState = {
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
  releasesEnvFilter: "",
  alerts: [],
  settings: null,
  systemHealth: null,
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

export const elements: AppElements = {
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

// Cached static DOM references shared across feature modules.
export const appFrame = document.querySelector<HTMLElement>(".app-frame");
export const loginScreen = document.getElementById("login-screen") as HTMLElement | null;
export const loginForm = document.getElementById("login-form") as HTMLFormElement | null;
export const loginError = document.getElementById("login-error") as HTMLElement | null;
export const userAvatarBtn = document.getElementById("user-avatar-btn") as HTMLButtonElement | null;
export const bbMenu = document.getElementById("bb-menu") as HTMLElement | null;
export const bbMenuUser = document.getElementById("bb-menu-user") as HTMLElement | null;
export const userAvatarInitial = document.getElementById("user-avatar-initial") as HTMLElement | null;
export const bbLogout = document.getElementById("bb-logout") as HTMLButtonElement | null;
export const sidebarToggle = document.getElementById("sidebar-toggle") as HTMLButtonElement | null;
export const envSelect = document.getElementById("env-select") as HTMLSelectElement | null;
export const pickerBtn = document.getElementById("project-picker-btn") as HTMLButtonElement | null;
export const pickerDropdown = document.getElementById("project-picker-dropdown") as HTMLElement | null;
export const pickerFilter = document.getElementById("project-picker-filter") as HTMLInputElement | null;
export const pickerList = document.getElementById("project-picker-list") as HTMLElement | null;
export const mobileMenuBtn = document.getElementById("mobile-menu-btn") as HTMLButtonElement | null;
export const mobileSidebar = document.getElementById("sidebar") as HTMLElement | null;
export const scopeBtn = document.getElementById("scope-btn");
export const scopeSheet = document.getElementById("scope-sheet");
export const scopeBackdrop = document.getElementById("scope-backdrop");
export const scopeClose = document.getElementById("scope-close");
export const scopeBody = document.getElementById("scope-sheet-body");

export function apiUrl(path: string): string {
  return path;
}

export function setStatus(message: string): void {
  elements.statusText.textContent = message;
}

export function showFlash(message: string, tone: "error" | "success" | "info" = "info", durationMs = 5000): void {
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

export function setRouteChip(message: string, tone = ""): void {
  elements.routeChip.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.routeChip.textContent = message;
}

export function setLiveStatus(message: string, tone = ""): void {
  elements.liveStatus.className = `chip${tone ? ` ${tone}` : ""}`;
  elements.liveStatus.textContent = message;
}

export function setActiveNav(): void {
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

export function setPageTitle(title: string): void {
  const h1 = document.getElementById("topbar-title");
  if (h1) h1.textContent = title;
  document.title = `${title} — BugBarn`;
}

export function setLoadingBar(active: boolean): void {
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

export function setActiveView(view: "overview" | "detail"): void {
  elements.overviewView.classList.toggle("hidden", view !== "overview");
  elements.detailView.classList.toggle("hidden", view !== "detail");
}

export function updateBBMenuUser(): void {
  if (bbMenuUser) {
    bbMenuUser.textContent = state.username || "BugBarn";
  }
  if (userAvatarInitial) {
    userAvatarInitial.textContent = (state.username || "?").charAt(0).toUpperCase();
  }
}

export function toTimestampMs(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  const date = new Date(value as string | number | Date);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}
