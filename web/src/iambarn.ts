// IAMBarn hosted web components. Instead of hand-rolling the signed-in-user UI
// against the IAMBarn API, we load IAMBarn's prebuilt, framework-agnostic widget
// bundle and mount its custom elements:
//   - <iambarn-user-menu>  — sidebar avatar + name + dropdown + RP-initiated logout
//   - <iambarn-profile>    — the full account/profile editor (our /account route)
//
// The bundle URL, client id, and post-logout redirect all come from the server's
// GET /api/v1/runtime-config (the same channel FunnelBarn/self-reporting use), so
// no IAMBarn hostname is ever hard-coded.
//
// The elements render only while the *browser* has a live IAMBarn SSO session,
// which can lapse (absolute TTL) even while BugBarn's own session is still valid.
// So we don't mount once and hope: we keep the sidebar in sync by re-checking
// the session on focus/visibility/interval — mounting the hosted menu when a
// session is present and revealing BugBarn's own fallback menu when it isn't,
// auto-recovering when the session returns. The account page additionally offers
// an on-demand, mostly-silent reconnect (OIDC prompt=none) to restore the session.

type IAMBarnRuntime = {
  serverURL?: string;
  clientID?: string;
  postLogoutRedirectURI?: string;
};

const WIDGET_SCRIPT_ID = "iambarn-widget-bundle";
const RECONNECT_PENDING_KEY = "iambarn_reconnect_pending";
const SYNC_INTERVAL_MS = 60_000;

let serverURL = "";
let clientID = "";
let postLogoutRedirectURI = "";
let scriptLoad: Promise<void> | null = null;
let menuMounted = false;
let syncing = false;

function isOIDCSession(): boolean {
  return document.cookie.split("; ").some((c) => c.startsWith("bugbarn_auth_method=oidc"));
}

async function fetchIAMBarnConfig(): Promise<IAMBarnRuntime | null> {
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return null;
    const cfg = await res.json() as { iambarn?: IAMBarnRuntime };
    return cfg?.iambarn ?? null;
  } catch {
    return null;
  }
}

// ensureConfig lazily loads and caches the IAMBarn runtime config.
async function ensureConfig(): Promise<boolean> {
  if (serverURL) return true;
  const cfg = await fetchIAMBarnConfig();
  if (!cfg?.serverURL || !cfg.clientID) return false;
  serverURL = cfg.serverURL;
  clientID = cfg.clientID;
  postLogoutRedirectURI = cfg.postLogoutRedirectURI ?? "";
  return true;
}

// ensureBundle injects {serverURL}/widget/iambarn-widget.iife.js exactly once and
// resolves when the custom elements are registered. Rejects if the script fails
// to load so callers can fall back to BugBarn's own UI.
function ensureBundle(): Promise<void> {
  if (scriptLoad) return scriptLoad;
  scriptLoad = new Promise<void>((resolve, reject) => {
    if (!serverURL) {
      reject(new Error("iambarn: serverURL not configured"));
      return;
    }
    const existing = document.getElementById(WIDGET_SCRIPT_ID) as HTMLScriptElement | null;
    if (existing) {
      resolve();
      return;
    }
    const script = document.createElement("script");
    script.id = WIDGET_SCRIPT_ID;
    script.src = `${serverURL}/widget/iambarn-widget.iife.js`;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`iambarn: failed to load widget bundle from ${script.src}`));
    document.head.appendChild(script);
  });
  return scriptLoad;
}

// hasLiveSession makes the same credentialed /api/v1/me call the widget makes, so
// we can decide whether the hosted UI will actually render before swapping it in.
async function hasLiveSession(): Promise<boolean> {
  if (!serverURL) return false;
  try {
    const res = await fetch(`${serverURL}/api/v1/me`, { credentials: "include" });
    return res.ok;
  } catch {
    return false;
  }
}

// reconnect starts a mostly-silent OIDC re-auth (prompt=none) via a top-level
// navigation — IAMBarn forbids iframing, so silent renewal has to be top-level.
// If the SSO session is still alive it returns here invisibly; if not, the server
// escalates to an interactive login. returnTo brings the user back to where they
// were (e.g. #/account).
function reconnect(returnTo: string): void {
  window.location.assign(`/api/v1/oidc/login?prompt=none&return_to=${encodeURIComponent(returnTo)}`);
}

function mountMenu(): void {
  if (menuMounted) return;
  const host = document.getElementById("iambarn-user-menu-host");
  if (!host) return;
  const menu = document.createElement("iambarn-user-menu");
  menu.setAttribute("server-url", serverURL);
  menu.setAttribute("account-href", "#/account");
  menu.setAttribute("client-id", clientID);
  if (postLogoutRedirectURI) {
    menu.setAttribute("post-logout-redirect-uri", postLogoutRedirectURI);
  }
  menu.setAttribute("show-email", "");
  host.replaceChildren(menu);
  host.removeAttribute("hidden");
  flipDropdownUpward(menu);
  // The hosted menu now owns the signed-in-user affordance; retire the custom one.
  document.getElementById("user-avatar-wrap")?.setAttribute("hidden", "");
  menuMounted = true;
}

// flipDropdownUpward makes the hosted user-menu open its dropdown ABOVE the
// avatar. The widget hardcodes the dropdown to open downward (top: 100%), but we
// mount it at the bottom of the sidebar, so downward would clip off the bottom of
// the viewport. The element uses an open shadow root, so we inject a scoped style
// override that opens it upward into the empty sidebar space instead.
function flipDropdownUpward(menu: HTMLElement): void {
  const inject = (): void => {
    const root = menu.shadowRoot;
    if (!root || root.getElementById("bb-menu-placement")) return;
    const style = document.createElement("style");
    style.id = "bb-menu-placement";
    style.textContent = ".menu-dropdown{top:auto !important;bottom:calc(100% + 6px) !important;}";
    root.appendChild(style);
  };
  inject();
  // The shadow root may not be attached on the same tick the element upgrades.
  if (!menu.shadowRoot) requestAnimationFrame(inject);
}

function unmountMenu(): void {
  if (!menuMounted) return;
  const host = document.getElementById("iambarn-user-menu-host");
  host?.replaceChildren();
  host?.setAttribute("hidden", "");
  // Restore BugBarn's own avatar menu (also exposes the fallback "Manage
  // account" / "Log out" path while the IAMBarn session is gone).
  document.getElementById("user-avatar-wrap")?.removeAttribute("hidden");
  menuMounted = false;
}

// syncMenu reconciles the sidebar with the live IAMBarn session: hosted menu when
// a session exists, BugBarn's fallback when it doesn't. Never redirects — that is
// only done on explicit user intent (the account page / a reconnect action).
async function syncMenu(): Promise<void> {
  if (syncing) return;
  syncing = true;
  try {
    const live = await hasLiveSession();
    if (live) {
      try {
        await ensureBundle();
        mountMenu();
      } catch {
        // Bundle unavailable — keep the fallback menu.
        unmountMenu();
      }
    } else {
      unmountMenu();
    }
  } finally {
    syncing = false;
  }
}

// initIAMBarnWidgets wires the sidebar sync loop. Called once at startup. No-op
// for local-password sessions (they keep the custom menu).
export async function initIAMBarnWidgets(): Promise<void> {
  if (!isOIDCSession()) return;
  if (!(await ensureConfig())) return;

  // Give OIDC sessions a fallback path to their account even before the hosted
  // menu mounts.
  document.getElementById("bb-account-link")?.removeAttribute("hidden");

  await syncMenu();

  const kick = (): void => { void syncMenu(); };
  window.addEventListener("focus", kick);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") kick();
  });
  window.setInterval(kick, SYNC_INTERVAL_MS);
}

// mountIAMBarnProfile renders the hosted <iambarn-profile> account editor into the
// given container (the /account view). If the IAMBarn session has lapsed it kicks
// off a mostly-silent reconnect instead of showing a blank panel, guarding
// against a redirect loop with a one-shot sessionStorage flag.
export async function mountIAMBarnProfile(container: HTMLElement): Promise<void> {
  if (!(await ensureConfig())) {
    container.textContent = "IAMBarn is not configured.";
    return;
  }

  if (await hasLiveSession()) {
    sessionStorage.removeItem(RECONNECT_PENDING_KEY);
    try {
      await ensureBundle();
    } catch {
      container.textContent = "Unable to load the IAMBarn account editor.";
      return;
    }
    const profile = document.createElement("iambarn-profile");
    profile.setAttribute("server-url", serverURL);
    container.replaceChildren(profile);
    return;
  }

  // No live session. If we haven't already bounced through a reconnect this
  // navigation, do so; otherwise offer a manual retry so we never loop.
  if (sessionStorage.getItem(RECONNECT_PENDING_KEY)) {
    sessionStorage.removeItem(RECONNECT_PENDING_KEY);
    renderReconnectPrompt(container);
    return;
  }
  sessionStorage.setItem(RECONNECT_PENDING_KEY, "1");
  container.textContent = "Reconnecting to IAMBarn…";
  reconnect(location.pathname + location.search + location.hash);
}

function renderReconnectPrompt(container: HTMLElement): void {
  container.replaceChildren();
  const p = document.createElement("p");
  p.textContent = "Your IAMBarn session has expired.";
  const btn = document.createElement("button");
  btn.type = "button";
  btn.textContent = "Reconnect to IAMBarn";
  btn.addEventListener("click", () => {
    reconnect(location.pathname + location.search + location.hash);
  });
  container.append(p, btn);
}
