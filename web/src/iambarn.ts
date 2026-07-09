// IAMBarn hosted web components. Instead of hand-rolling the signed-in-user UI
// against the IAMBarn API, we load IAMBarn's prebuilt, framework-agnostic widget
// bundle and mount its custom elements:
//   - <iambarn-user-menu>  — sidebar avatar + name + dropdown + RP-initiated logout
//   - <iambarn-profile>    — the full account/profile editor (our /account route)
//
// The bundle URL, client id, and post-logout redirect all come from the server's
// GET /api/v1/runtime-config (the same channel FunnelBarn/self-reporting use), so
// no IAMBarn hostname is ever hard-coded. The elements render nothing unless the
// browser already has an IAMBarn session, so we only bother loading them for
// sessions that came in via OIDC (the bugbarn_auth_method=oidc hint cookie).

type IAMBarnRuntime = {
  serverURL?: string;
  clientID?: string;
  postLogoutRedirectURI?: string;
};

const WIDGET_SCRIPT_ID = "iambarn-widget-bundle";

let serverURL = "";
let scriptLoad: Promise<void> | null = null;

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

// initIAMBarnWidgets loads the bundle and swaps the sidebar's custom avatar menu
// for the hosted <iambarn-user-menu>. Called once at startup. No-op for
// local-password sessions (they keep the custom menu) and degrades gracefully to
// the custom menu if the bundle can't be loaded.
export async function initIAMBarnWidgets(): Promise<void> {
  if (!isOIDCSession()) return;

  const cfg = await fetchIAMBarnConfig();
  if (!cfg?.serverURL || !cfg.clientID) return;
  serverURL = cfg.serverURL;

  const host = document.getElementById("iambarn-user-menu-host");
  if (!host) return;

  try {
    await ensureBundle();
  } catch {
    // Bundle unavailable — leave BugBarn's own avatar menu in place.
    return;
  }

  const menu = document.createElement("iambarn-user-menu");
  menu.setAttribute("server-url", serverURL);
  menu.setAttribute("account-href", "#/account");
  menu.setAttribute("client-id", cfg.clientID);
  if (cfg.postLogoutRedirectURI) {
    menu.setAttribute("post-logout-redirect-uri", cfg.postLogoutRedirectURI);
  }
  menu.setAttribute("show-email", "");
  host.appendChild(menu);
  host.removeAttribute("hidden");

  // The hosted menu now owns the signed-in-user affordance; retire the custom one.
  document.getElementById("user-avatar-wrap")?.setAttribute("hidden", "");
}

// mountIAMBarnProfile renders the hosted <iambarn-profile> account editor into the
// given container (the /account view). Ensures the bundle is loaded first.
export async function mountIAMBarnProfile(container: HTMLElement): Promise<void> {
  if (!serverURL) {
    const cfg = await fetchIAMBarnConfig();
    if (cfg?.serverURL) serverURL = cfg.serverURL;
  }
  if (!serverURL) {
    container.textContent = "IAMBarn is not configured.";
    return;
  }
  try {
    await ensureBundle();
  } catch {
    container.textContent = "Unable to load the IAMBarn account editor.";
    return;
  }
  const profile = document.createElement("iambarn-profile");
  profile.setAttribute("server-url", serverURL);
  container.replaceChildren(profile);
}
