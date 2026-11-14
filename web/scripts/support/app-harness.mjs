// Boots the real SPA (src/bootstrap.ts) in jsdom against a stubbed fetch, and
// offers helpers to navigate by hash and to drive what a view rendered.
import { after } from "node:test";
import { win, settle, navigations } from "./dom-env.mjs";

export { win, navigations };

const now = Date.now();
const iso = (minutesAgo) => new Date(now - minutesAgo * 60000).toISOString();

const issue = (n, extra = {}) => ({
  id: `issue-${n}`,
  title: `TypeError number ${n}`,
  normalizedTitle: `TypeError number ${n}`,
  exceptionType: "TypeError",
  fingerprint: `fp-${n}`,
  status: "unresolved",
  event_count: 10 * n,
  first_seen: iso(600),
  last_seen: iso(5),
  project_slug: "web",
  ...extra,
});

const event = (n, extra = {}) => ({
  id: `event-${n}`,
  issueId: "issue-1",
  title: `TypeError number 1`,
  message: "Cannot read properties of undefined",
  severityText: "ERROR",
  timestamp: iso(n),
  exception: {
    type: "TypeError",
    message: "Cannot read properties of undefined",
    stacktrace: [
      { function: "render", file: "app.js", line: 10, column: 4, in_app: true },
      { function: "main", file: "lib.js", line: 3, column: 1 },
    ],
  },
  user: { id: "u1", email: "a@example.com" },
  breadcrumbs: [{ timestamp: iso(n), category: "ui.click", message: "clicked", level: "info" }],
  attributes: { environment: "production", release: "1.0.0" },
  ...extra,
});

const fixtures = {
  me: { authEnabled: true, authenticated: true, username: "wiebe" },
  projects: [
    { id: 1, slug: "web", name: "Web", status: "active", issue_count: 3, event_count: 40, log_count: 5, group_id: 1, retention_days: null, sampling_mode: "", issue_status: { open: 2, regressed: 0, resolved: 1, muted: 0, all: 3 } },
    { id: 2, slug: "api", name: "API", status: "pending", issue_count: 0, event_count: 0, log_count: 0, group_id: null, retention_days: 14, sampling_mode: "on" },
  ],
  issues: [issue(1), issue(2, { status: "resolved" }), issue(3, { mute_mode: "forever", status: "muted" })],
  counts: { open: 2, regressed: 1, resolved: 1, muted: 1, all: 5 },
};

/** The default route table. Each handler may be overridden per test. */
export function defaultRoutes(overrides = {}) {
  const routes = {
    "GET /api/v1/me": () => fixtures.me,
    "GET /api/v1/runtime-config": { oidc: {}, funnelbarn: {}, bugbarn: {}, spanbarn: {} },
    "GET /api/v1/projects/pending-count": { count: 1 },
    "GET /api/v1/projects": () => ({ projects: fixtures.projects }),
    "GET /api/v1/facets/attributes.environment": { values: ["production", "staging"], items: [{ value: "production", count: 3 }] },
    "GET /api/v1/issues/sparklines": () => ({ sparklines: { "issue-1": [1, 2, 3, 0, 5] } }),
    "GET /api/v1/issues/issue-1/releases": { releases: [{ release: "1.0.0", events: 4 }], sampled_events: 4 },
    "GET /api/v1/issues/issue-1/events": () => ({ events: [event(1), event(2)], hasMore: true }),
    "GET /api/v1/issues/issue-1": () => ({ issue: issue(1, { first_release: "1.0.0" }) }),
    "GET /api/v1/issues": () => ({ issues: fixtures.issues, hasMore: false, total: 3, counts: fixtures.counts }),
    "GET /api/v1/events/timeline": { range: "24h", bucket_seconds: 3600, buckets: [{ start: iso(120), counts: { web: 3, api: 1 } }, { start: iso(60), counts: { web: 7 } }], projects: [{ slug: "web", name: "Web", total: 10 }, { slug: "api", name: "API", total: 1 }] },
    "GET /api/v1/events/event-1": () => ({ event: event(1) }),
    "GET /api/v1/surfaces": { surfaces: [{ surface: "mcp", issues_24h: 2, issues_7d: 5, open: 1 }] },
    "GET /api/v1/releases": { releases: [{ id: "r1", name: "1.0.0", environment: "production", observedAt: iso(120), version: "1.0.0", commitSha: "abc1234", createdBy: "ci" }] },
    "GET /api/v1/alerts": { alerts: [{ id: "a1", name: "Spike", enabled: true, condition: "new_issue", threshold: 5, cooldown_minutes: 10, webhook_url: "https://hooks.test/x", project_slug: "web" }] },
    "GET /api/v1/settings": { settings: { username: "wiebe", displayName: "Wiebe", timezone: "Europe/Amsterdam", liveWindowMinutes: 15 } },
    "GET /api/v1/apikeys": { apiKeys: [{ id: 1, name: "ci", scope: "ingest", projectId: 1, createdAt: iso(900), lastUsedAt: iso(3) }] },
    "GET /api/v1/groups": { groups: [{ id: 1, name: "Frontend", slug: "frontend", created_at: iso(1000) }] },
    "GET /api/v1/aliases": { aliases: [{ alias_slug: "site", project_id: 1, project_slug: "web" }] },
    "GET /api/v1/key-requests": { keyRequests: [{ id: "kr1", project: "web", label: "laptop", createdAt: iso(10), expiresAt: iso(-50) }] },
    "GET /api/v1/health": { status: "ok", ingest: { healthy: true, lastEventAgeSeconds: 4, hasEvents: true, queueDepth: 0, queueDepthKnown: true, walSizeBytes: 2048 } },
    "GET /api/v1/logs/histogram": { from: iso(60), to: iso(0), bucket_seconds: 60, buckets: [{ start: iso(30), counts: { info: 3, error: 1 } }], truncated: false },
    "GET /api/v1/logs/facets": { key: "", items: [{ value: "service", count: 4 }], truncated: false },
    "GET /api/v1/logs": { logs: [{ id: 1, received_at: iso(2), level_num: 30, level: "info", message: "hello", data: { service: "api" }, project_slug: "web" }, { id: 2, received_at: iso(1), level_num: 50, level: "error", message: "boom", project_slug: "web" }] },
    "GET /api/v1/telemetry/security": () => ({
      rows: [
        { id: 1, ts: iso(3), source: "sshd", host: "h1", kind: "ssh_failed", src_ip: "1.2.3.4", user: "root", action: "login", status: 0, method: "", path: "", message: "failed login", raw: "{\"a\":1}" },
        { id: 2, ts: iso(2), source: "traefik", host: "h2", kind: "http", src_ip: "5.6.7.8", user: "", action: "", status: 404, method: "GET", path: "/x", message: "", raw: "plain line" },
      ],
      next_cursor: "c1",
    }),
    "GET /api/v1/telemetry/hosts/h1/metrics": { metrics: ["cpu.util", "load1", "mem.avail_pct", "fs./.used_pct", "net.eth0.rx_bytes_per_s"] },
    "GET /api/v1/telemetry/hosts/h1/series": ({ url }) => ({ metric: decodeURIComponent(url.match(/metric=([^&]*)/)[1]), resolution: "1m", points: [{ ts: iso(30), value: 10.5, min: 9, max: 12 }, { ts: iso(20), value: 2048, min: 1, max: 3000 }] }),
    "GET /api/v1/telemetry/hosts": { hosts: [{ host: "h1", cores: 4, lastSeen: iso(1) }, { host: "h2", cores: 0, lastSeen: iso(600) }] },
    "GET /api/v1/detection/rules": { rules: [
      { id: "ssh", name: "ssh brute force", enabled: true, severity: "WARNING", track: "security", threshold: 5, window: "5m", group_by: ["src_ip"], builtin: true, overridden: false },
      { id: "cpu", name: "cpu hot", enabled: false, severity: "ERROR", track: "metrics", metric: "cpu.util", op: "gt", per_core: true, threshold: 2, for: "5m", builtin: true, overridden: true, updated_at: iso(5) },
      { id: "hb", name: "heartbeat", enabled: true, severity: "ERROR", track: "heartbeat", window: "5m", threshold: 0, builtin: false, overridden: false },
    ] },
    "GET /api/v1/monitors": { monitors: [
      { slug: "nightly", project: "web", state: "ok", lastStatus: "ok", intervalSeconds: 0, graceSeconds: 300, schedule: "0 2 * * *", timezone: "Europe/Amsterdam", dueBy: iso(-600), stateSince: iso(300), lastCheckInAt: iso(30) },
      { slug: "worker", project: "web", state: "missed", lastStatus: "ok", intervalSeconds: 3600, graceSeconds: 60, dueBy: iso(10), stateSince: iso(10) },
      { slug: "sync", project: "api", state: "error", lastStatus: "error", intervalSeconds: 90, graceSeconds: 0, dueBy: iso(1), stateSince: iso(1) },
    ] },
    "GET /api/v1/setup/": { setup: "ok" },
    "GET /api/v1/projects/web/limits": { retention_days: null },
    "GET /api/v1/events/stream": { ok: true },
  };
  return { ...routes, ...overrides };
}

class FakeEventSource {
  static instances = [];
  constructor(url) {
    this.url = url;
    this.closed = false;
    FakeEventSource.instances.push(this);
  }
  close() {
    this.closed = true;
  }
  emit(data) {
    this.onmessage?.({ data: typeof data === "string" ? data : JSON.stringify(data) });
  }
}


/** Installs the browser globals jsdom lacks. */
function installShims() {
  win.EventSource = FakeEventSource;
  globalThis.EventSource = FakeEventSource;
  win.confirm = () => true;
  globalThis.confirm = win.confirm;
  win.prompt = () => "answer";
  globalThis.prompt = win.prompt;
  win.alert = () => {};
  globalThis.alert = win.alert;
  const clipboard = { written: [], async writeText(text) { this.written.push(text); } };
  Object.defineProperty(win.navigator, "clipboard", { value: clipboard, configurable: true });
  globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
  globalThis.MutationObserver = win.MutationObserver;
  win.scrollTo = () => {};
  win.URL.createObjectURL = () => "blob:test";
  win.URL.revokeObjectURL = () => {};
  win.HTMLElement.prototype.scrollIntoView = () => {};
  win.matchMedia ??= () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  win.crypto ??= globalThis.crypto;
  win.performance ??= globalThis.performance;
  win.EventSource = FakeEventSource;
  return clipboard;
}

/** Boots the app and waits until the first load settles. */
export async function boot(routes = defaultRoutes(), hash = "#/issues") {
  installShims();
  // Timers the app starts must not keep the test process alive.
  const realInterval = globalThis.setInterval;
  globalThis.setInterval = (...args) => realInterval(...args).unref();
  after(() => win.close());
  const { stubFetch } = await import("./dom-env.mjs");
  const fetchStub = stubFetch(routes);
  globalThis.fetch = (...args) => win.fetch(...args);
  win.location.hash = hash;
  await import("../../src/bootstrap.ts");
  await settleAll();
  return fetchStub;
}

export async function settleAll(times = 6) {
  for (let i = 0; i < times; i++) await settle();
}

/** Navigates by hash and waits for the router and its loads. */
export async function go(hash) {
  win.location.hash = hash;
  await settleAll();
}

export const text = (selector = "body") => win.document.querySelector(selector)?.textContent ?? "";
export const q = (selector, root = win.document) => root.querySelector(selector);
export const qa = (selector, root = win.document) => [...root.querySelectorAll(selector)];

export function click(el) {
  el.dispatchEvent(new win.MouseEvent("click", { bubbles: true, cancelable: true }));
}

export function type(el, value, eventName = "input") {
  el.value = value;
  el.dispatchEvent(new win.Event(eventName, { bubbles: true }));
}

export function pick(el, value) {
  el.value = value;
  el.dispatchEvent(new win.Event("change", { bubbles: true }));
}

/**
 * Presses every button and checkbox a view rendered, one at a time, re-reading
 * the DOM after each press because most handlers re-render. A button is pressed
 * once per label. Returns the labels pressed.
 */
export async function pressAll(rootSelector, { skip = /^$/ } = {}) {
  const pressed = new Set();
  for (let round = 0; round < 60; round++) {
    const root = win.document.querySelector(rootSelector);
    if (!root) break;
    const next = qa("button, [role=button], input[type=checkbox], summary", root).find((el) => {
      const key = `${el.tagName}:${el.id}:${el.getAttribute("data-action") ?? ""}:${el.textContent.trim()}:${el.getAttribute("aria-label") ?? ""}`;
      if (pressed.has(key) || el.disabled || skip.test(key)) return false;
      pressed.add(key);
      return true;
    });
    if (!next) break;
    click(next);
    await settleAll(3);
  }
  return [...pressed];
}
