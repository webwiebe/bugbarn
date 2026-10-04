// Shared fetch for the telemetry read endpoints (security search, hosts).
// Unlike fetchJson it keeps the server's message on an error response, so a
// 503 ("has not been created yet") reaches the page as written.
import { state } from "./core.js";
import { apiFetch, renderLogin } from "./http.js";

export async function telemetryGet<T>(path: string): Promise<T> {
  const res = await apiFetch(path);
  if (res.status === 401) {
    state.authRequired = true;
    state.authenticated = false;
    renderLogin();
  }
  if (!res.ok) {
    const text = (await res.text().catch(() => "")).trim();
    throw new Error(text || `${res.status} ${res.statusText}`.trim());
  }
  return await res.json() as T;
}

/** isStale reports whether a host has been silent longer than the heartbeat
 * window (5 minutes, the same as the built-in "host silent" rule). */
export function isStale(lastSeen: string, now = Date.now()): boolean {
  const t = Date.parse(lastSeen);
  return !Number.isFinite(t) || now - t > 5 * 60 * 1000;
}
