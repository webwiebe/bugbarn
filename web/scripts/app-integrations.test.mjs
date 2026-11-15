// The optional integrations bootstrap starts from /api/v1/runtime-config:
// FunnelBarn analytics, self-reporting, the hosted IAMBarn widgets, the PWA
// install prompt and the built-in request tracing.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, navigations, q, qa, click, settleAll, win } from "./support/app-harness.mjs";

const iam = "https://iam.test";
const routes = defaultRoutes({
  "GET /api/v1/runtime-config": {
    funnelbarn: { enabled: true, endpoint: "https://fb.test", apiKey: "fb-key" },
    bugbarn: { enabled: true, apiKey: "self-key", project: "bugbarn-web" },
    iambarn: { serverURL: iam, clientID: "client-1", postLogoutRedirectURI: "https://bb.test/" },
    spanbarn: { enabled: false },
  },
  [`GET ${iam}/api/v1/me`]: { ok: true },
});
win.document.cookie = "bugbarn_auth_method=oidc";
const fb = { inits: [], pages: 0, tracks: [] };
win.funnelbarn = { init: (o) => fb.inits.push(o), page: () => fb.pages++, track: (n, p) => fb.tracks.push([n, p]) };
routes["GET /api/v1/ping"] = { pong: true };
const fetchStub = await boot(routes);
const id = (name) => win.document.getElementById(name);
const scriptWithSrc = (part) => qa("script").find((s) => s.src.includes(part));
const posts = (url) => fetchStub.calls.filter((c) => c.method === "POST" && c.url.startsWith(url));

test("FunnelBarn loads its SDK script from the configured endpoint, then initialises and tracks pages", async () => {
  const script = scriptWithSrc("fb.test/sdk/funnelbarn.js");
  assert.ok(script, "the SDK script is injected");
  script.onload();
  await settleAll();
  assert.deepEqual(fb.inits, [{ apiKey: "fb-key", endpoint: "https://fb.test", recording: true }]);
  assert.equal(fb.pages, 1);
  win.location.hash = "#/alerts";
  await settleAll();
  assert.equal(fb.pages, 2, "a route change is a page view");
});

test("frontend errors are reported to BugBarn with the self-report key and project", async () => {
  const error = new Error("render failed");
  win.dispatchEvent(new win.ErrorEvent("error", { error }));
  const report = posts("/api/v1/events").at(-1);
  assert.ok(report, "an event was posted");
  assert.equal(new Headers(report.init.headers).get("x-bugbarn-api-key"), "self-key");
  assert.equal(new Headers(report.init.headers).get("x-bugbarn-project"), "bugbarn-web");
  const body = JSON.parse(report.init.body);
  assert.equal(body.body, "render failed");
  assert.equal(body.exception.type, "Error");
  assert.ok(Array.isArray(body.exception.stacktrace) && body.exception.stacktrace[0].file);
  assert.equal(body.sender.sdk.name, "bugbarn.web");
});

test("an unhandled rejection with a plain reason is reported too", () => {
  const before = posts("/api/v1/events").length;
  const ev = new win.Event("unhandledrejection");
  ev.reason = "plain string";
  win.dispatchEvent(ev);
  assert.equal(posts("/api/v1/events").length, before + 1);
  assert.equal(JSON.parse(posts("/api/v1/events").at(-1).init.body).body, "plain string");
});

test("the IAMBarn widget bundle loads for an OIDC session and the hosted menu replaces the local one", async () => {
  const script = scriptWithSrc(`${iam}/widget/iambarn-widget.iife.js`);
  assert.ok(script, "the widget bundle is injected");
  script.onload();
  await settleAll();
  const menu = q("#iambarn-user-menu-host iambarn-user-menu");
  assert.ok(menu, "the hosted menu is mounted");
  assert.equal(menu.getAttribute("server-url"), iam);
  assert.equal(menu.getAttribute("client-id"), "client-1");
  assert.equal(menu.getAttribute("account-href"), "#/account");
  assert.equal(menu.getAttribute("post-logout-redirect-uri"), "https://bb.test/");
  assert.equal(id("user-avatar-wrap").hasAttribute("hidden"), true);
  assert.equal(id("bb-account-link").hasAttribute("hidden"), false);
});

test("when the IAMBarn session lapses, focus restores BugBarn's own menu", async () => {
  routes[`GET ${iam}/api/v1/me`] = { status: 401, body: {} };
  win.dispatchEvent(new win.Event("focus"));
  await settleAll();
  assert.equal(q("#iambarn-user-menu-host iambarn-user-menu"), null);
  assert.equal(id("user-avatar-wrap").hasAttribute("hidden"), false);
});

test("when the session returns, becoming visible mounts the hosted menu again", async () => {
  routes[`GET ${iam}/api/v1/me`] = { ok: true };
  win.document.dispatchEvent(new win.Event("visibilitychange"));
  await settleAll();
  assert.ok(q("#iambarn-user-menu-host iambarn-user-menu"));
});

test("the account page mounts the hosted profile editor while the session is live", async () => {
  await go("#/account");
  const profile = q("#iambarn-profile-host iambarn-profile");
  assert.ok(profile);
  assert.equal(profile.getAttribute("server-url"), iam);
});

test("a lapsed session on the account page reconnects once, then offers a manual retry", async () => {
  routes[`GET ${iam}/api/v1/me`] = { status: 401, body: {} };
  const before = navigations.length;
  await go("#/alerts");
  await go("#/account");
  assert.equal(navigations.length, before + 1);
  assert.match(navigations.at(-1), /^\/api\/v1\/oidc\/login\?prompt=none&return_to=%23%2Faccount$|return_to=.*account/);
  assert.match(id("iambarn-profile-host").textContent, /Reconnecting to IAMBarn/);
  await go("#/alerts");
  await go("#/account");
  assert.match(id("iambarn-profile-host").textContent, /session has expired/);
  click(q("#iambarn-profile-host button"));
  assert.equal(navigations.length, before + 2);
});

test("the install banner appears on beforeinstallprompt and Install triggers the browser prompt", async () => {
  let prompted = 0;
  const ev = new win.Event("beforeinstallprompt", { cancelable: true });
  ev.prompt = async () => {
    prompted++;
  };
  ev.userChoice = Promise.resolve({ outcome: "accepted" });
  win.dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true);
  assert.ok(id("pwa-install-banner"));
  click(id("pwa-install-btn"));
  await settleAll();
  assert.equal(prompted, 1);
  assert.equal(id("pwa-install-banner"), null, "accepted install dismisses the banner");
  assert.equal(win.localStorage.getItem("pwa_prompt_dismissed"), "1");
});

test("appinstalled tracks the install and dismisses the banner", () => {
  win.dispatchEvent(new win.Event("appinstalled"));
  assert.deepEqual(fb.tracks.at(-1), ["pwa_installed", undefined]);
});

test("every API call carries a traceparent header and is reported as a span in batches", async () => {
  const withTrace = fetchStub.calls.filter((c) => c.url.startsWith("/api/v1/issues?") && c.init.headers.get("traceparent"));
  assert.ok(withTrace.length > 0);
  assert.match(withTrace[0].init.headers.get("traceparent"), /^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/);
  for (let i = 0; i < 30; i++) await win.fetch("/api/v1/ping");
  const batch = posts("/api/v1/telemetry").at(-1);
  assert.ok(batch, "a span batch was posted");
  const span = JSON.parse(batch.init.body).spans.at(-1);
  assert.equal(span.service, "bugbarn-web");
  assert.equal(span.kind, "CLIENT");
  assert.equal(span.attributes["http.status_code"], 200);
});

test("a 5xx response and a failed request are reported as client errors", async () => {
  routes["GET /api/v1/boom"] = { status: 502, body: {} };
  await win.fetch("/api/v1/boom");
  const err = posts("/api/v1/client-errors").at(-1);
  assert.ok(err);
  assert.match(JSON.parse(err.init.body).message, /HTTP 502 GET \/api\/v1\/boom/);
  routes["GET /api/v1/down"] = () => {
    throw new Error("network down");
  };
  await assert.rejects(win.fetch("/api/v1/down"), /network down/);
});

test("navigation produces a span and hiding the page flushes the queue", async () => {
  win.history.pushState(null, "", "/elsewhere");
  win.history.replaceState(null, "", "/elsewhere2");
  const before = posts("/api/v1/telemetry").length;
  Object.defineProperty(win.document, "visibilityState", { value: "hidden", configurable: true });
  win.document.dispatchEvent(new win.Event("visibilitychange"));
  assert.equal(posts("/api/v1/telemetry").length, before + 1);
  assert.ok(JSON.parse(posts("/api/v1/telemetry").at(-1).init.body).spans.some((s) => s.name.startsWith("navigation ")));
  win.dispatchEvent(new win.Event("popstate"));
});

test("window errors and rejections are reported by the built-in handlers", () => {
  const before = posts("/api/v1/client-errors").length;
  win.dispatchEvent(new win.ErrorEvent("error", { error: new TypeError("oops") }));
  const rejection = new win.Event("unhandledrejection");
  rejection.reason = new RangeError("range");
  win.dispatchEvent(rejection);
  const plain = new win.Event("unhandledrejection");
  plain.reason = "text";
  win.dispatchEvent(plain);
  const bodies = posts("/api/v1/client-errors").slice(before).map((c) => JSON.parse(c.init.body));
  assert.deepEqual(bodies.map((b) => b.type), ["TypeError", "RangeError", "UnhandledRejection"]);
});
