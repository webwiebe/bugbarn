// Application bootstrap: wires all the top-level DOM event listeners (sidebar,
// avatar menu, mobile nav, project picker, scope sheet, env switcher, login
// form), starts instrumentation and self-reporting, and kicks off start().
import { initInstrumentation } from "./instrumentation.js";
import {
  apiUrl,
  appFrame,
  bbLogout,
  bbMenu,
  elements,
  envKey,
  envSelect,
  loginForm,
  mobileMenuBtn,
  mobileSidebar,
  pickerBtn,
  pickerDropdown,
  pickerFilter,
  pickerList,
  scopeBackdrop,
  scopeBtn,
  scopeClose,
  sidebarKey,
  sidebarToggle,
  state,
  updateBBMenuUser,
  userAvatarBtn,
  type OIDCRuntime,
} from "./core.js";
import { login, loadSession, logout, renderLogin } from "./http.js";
import { requestNotificationPermission, stopLiveStream } from "./live.js";
import { refreshAll, route } from "./router.js";
import {
  checkPendingProjects,
  closePicker,
  closeScopeSheet,
  loadEnvironments,
  loadProjects,
  openPicker,
  openScopeSheet,
  renderEnvSwitcher,
  renderPickerList,
} from "./views-projects.js";
import { loadIssues } from "./views-issues.js";

initInstrumentation();

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
  userAvatarBtn?.setAttribute("aria-expanded", "false");
}

userAvatarBtn?.addEventListener("click", (ev) => {
  ev.stopPropagation();
  const isHidden = bbMenu?.hasAttribute("hidden");
  if (isHidden) {
    bbMenu?.removeAttribute("hidden");
    userAvatarBtn?.setAttribute("aria-expanded", "true");
  } else {
    closeBBMenu();
  }
});

document.addEventListener("click", closeBBMenu);

document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") {
    closeBBMenu();
    userAvatarBtn?.focus();
  }
});

bbLogout?.addEventListener("click", () => {
  void logout();
});

loginForm?.addEventListener("submit", (event) => {
  event.preventDefault();
  const formData = new FormData(loginForm as HTMLFormElement);
  void login(String(formData.get("username") || ""), String(formData.get("password") || ""));
});

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
  renderPickerList((pickerFilter as HTMLInputElement).value);
});

pickerFilter?.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { closePicker(); pickerBtn?.focus(); }
  if (e.key === "ArrowDown") { (pickerList?.querySelector<HTMLButtonElement>(".picker-item") )?.focus(); e.preventDefault(); }
});

pickerList?.addEventListener("keydown", (e) => {
  const items = Array.from((pickerList as HTMLElement).querySelectorAll<HTMLButtonElement>(".picker-item"));
  const idx = items.indexOf(document.activeElement as HTMLButtonElement);
  if (e.key === "ArrowDown" && idx < items.length - 1) { items[idx + 1].focus(); e.preventDefault(); }
  if (e.key === "ArrowUp") { if (idx > 0) items[idx - 1].focus(); else pickerFilter?.focus(); e.preventDefault(); }
  if (e.key === "Escape") { closePicker(); pickerBtn?.focus(); }
});

envSelect?.addEventListener("change", () => {
  const env = (envSelect as HTMLSelectElement).value;
  state.currentEnv = env;
  localStorage.setItem(envKey, env);
  void loadIssues();
});

scopeBtn?.addEventListener("click", openScopeSheet);
scopeClose?.addEventListener("click", closeScopeSheet);
scopeBackdrop?.addEventListener("click", closeScopeSheet);

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
  void initIAMBarnLinks();

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

async function initIAMBarnLinks(): Promise<void> {
  // Only show IAMBarn links when the session was established via OIDC.
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
  const profileLink = document.getElementById("bb-iambarn-profile") as HTMLAnchorElement | null;
  if (profileLink && cfg.iambarn?.profileURL) {
    profileLink.href = cfg.iambarn.profileURL;
    profileLink.removeAttribute("hidden");
  }
  const logoutLink = document.getElementById("bb-iambarn-logout") as HTMLAnchorElement | null;
  if (logoutLink && cfg.oidc?.endSessionURL) {
    const ret = `${window.location.origin}/`;
    const sep = cfg.oidc.endSessionURL.includes("?") ? "&" : "?";
    logoutLink.href = `${cfg.oidc.endSessionURL}${sep}post_logout_redirect_uri=${encodeURIComponent(ret)}`;
    logoutLink.removeAttribute("hidden");
  }
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
