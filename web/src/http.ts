// HTTP layer: authenticated fetch helpers (CSRF retry, project/group headers),
// session bootstrap, login/logout, and the login screen / OIDC redirect flow.
import { normalizeObject, readString } from "./data.js";
import { errorMessage } from "./format.js";
import type { RawRecord } from "./types.js";
import {
  apiUrl,
  appFrame,
  httpUnauthorized,
  loginError,
  loginForm,
  loginScreen,
  setStatus,
  state,
  updateBBMenuUser,
  type OIDCRuntime,
} from "./core.js";
import { loadProjects } from "./views-projects.js";
import { refreshAll } from "./router.js";
import { stopLiveStream } from "./live.js";

export async function loadSession(): Promise<void> {
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

export async function logout(): Promise<void> {
  // Snapshot before /api/v1/logout clears the hint cookie — if this session
  // came from iambarn we must also end the IdP session, otherwise the SPA's
  // login screen would auto-redirect back into iambarn, iambarn would reuse
  // its still-valid session, and the user would bounce straight back in.
  const wasOIDC = document.cookie.split("; ").some((c) => c.startsWith("bugbarn_auth_method=oidc"));
  let serverLogoutURL = "";
  try {
    const res = await fetch(apiUrl("/api/v1/logout"), { method: "POST", credentials: "include" });
    if (res.ok) {
      const payload = normalizeObject<RawRecord>(await res.json());
      serverLogoutURL = readString(payload, ["logout_url"]);
    }
  } catch {
    // ignore network errors on logout
  }
  window.funnelbarn?.track("logout", state.username ? { username: state.username } : undefined);
  state.authenticated = false;
  state.username = "";
  stopLiveStream();
  // Server-driven RP-initiated logout: the backend revoked our tokens and
  // built the end-session URL (with id_token_hint) — follow it so the
  // iambarn session ends too, without any confirmation prompt.
  if (serverLogoutURL) {
    window.location.assign(serverLogoutURL);
    return;
  }
  if (wasOIDC) {
    // Fallback for older servers that don't return logout_url.
    const endURL = await fetchIAMBarnEndSessionURL();
    if (endURL) {
      window.location.assign(endURL);
      return;
    }
  }
  renderLogin();
}

export async function fetchIAMBarnEndSessionURL(): Promise<string> {
  try {
    const res = await fetch("/api/v1/runtime-config");
    if (!res.ok) return "";
    const cfg = await res.json() as { oidc?: OIDCRuntime };
    return cfg?.oidc?.endSessionURL ?? "";
  } catch {
    return "";
  }
}

export async function fetchJson(path: string, allowMissing = false): Promise<unknown> {
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

export function getCSRFToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)bugbarn_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

export async function refreshCSRFToken(): Promise<void> {
  await fetch(apiUrl("/api/v1/me"), { credentials: "include", headers: { Accept: "application/json" } });
}

export async function postJson(path: string, body: unknown, _retried = false): Promise<unknown> {
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

export async function deleteJson(path: string): Promise<unknown> {
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

export async function postFormData(path: string, formData: FormData): Promise<unknown> {
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

export async function apiFetch(path: string, init: RequestInit = {}, _retried = false): Promise<Response> {
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

export async function login(username: string, password: string): Promise<void> {
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
      window.funnelbarn?.track("login_failed", { reason: "invalid_credentials" });
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
    window.funnelbarn?.track("login", state.username ? { username: state.username } : undefined);
    setStatus(state.username ? `Logged in as ${state.username}.` : "Logged in.");
    await Promise.all([loadProjects(), refreshAll()]);
  } catch (error) {
    window.funnelbarn?.track("login_failed", { reason: errorMessage(error) });
    renderLogin(errorMessage(error));
  }
}

export function renderLogin(error = ""): void {
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
export async function maybeRedirectToOIDC(): Promise<void> {
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
