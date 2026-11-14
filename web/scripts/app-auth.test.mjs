// Login, logout, OIDC hand-off and the authenticated fetch helpers.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, navigations, q, click, settleAll, win } from "./support/app-harness.mjs";

const routes = defaultRoutes({ "GET /api/v1/me": { status: 401, body: {} } });
const tracked = [];
win.funnelbarn = { init() {}, page() {}, track: (name, props) => tracked.push([name, props]) };
const fetchStub = await boot(routes);
const http = await import("../src/http.ts");
const core = await import("../src/core.ts");
const id = (name) => win.document.getElementById(name);
const callsTo = (method, url) => fetchStub.calls.filter((c) => c.method === method && c.url.startsWith(url));
const submitLogin = async (username, password) => {
  const form = id("login-form");
  form.querySelector("input[name=username]").value = username;
  form.querySelector("input[name=password]").value = password;
  form.dispatchEvent(new win.Event("submit", { bubbles: true, cancelable: true }));
  await settleAll();
};

test("a 401 from /me shows the login screen and hides the app", () => {
  assert.equal(id("login-screen").hidden, false);
  assert.equal(q(".app-frame").hidden, true);
  assert.equal(core.state.authRequired, true);
});

test("wrong credentials show the error and track login_failed", async () => {
  routes["POST /api/v1/login"] = { status: 401, body: {} };
  await submitLogin("wiebe", "wrong");
  assert.equal(id("login-error").hidden, false);
  assert.equal(id("login-error").textContent, "Invalid username or password.");
  assert.deepEqual(tracked.at(-1), ["login_failed", { reason: "invalid_credentials" }]);
});

test("a network failure during login shows its message", async () => {
  routes["POST /api/v1/login"] = () => {
    throw new Error("offline");
  };
  await submitLogin("wiebe", "x");
  assert.equal(id("login-error").textContent, "offline");
});

test("valid credentials hide the login screen, name the user and load the app", async () => {
  routes["POST /api/v1/login"] = { authEnabled: true, authenticated: true, username: "wiebe" };
  routes["GET /api/v1/me"] = { authEnabled: true, authenticated: true, username: "wiebe" };
  await submitLogin("wiebe", "right");
  assert.equal(id("login-screen").hidden, true);
  assert.equal(q(".app-frame").hidden, false);
  assert.equal(core.state.username, "wiebe");
  assert.equal(id("bb-menu-user").textContent, "wiebe");
  assert.deepEqual(tracked.find((t) => t[0] === "login"), ["login", { username: "wiebe" }]);
  assert.ok(callsTo("GET", "/api/v1/issues?").length > 0);
  const body = JSON.parse(callsTo("POST", "/api/v1/login").at(-1).init.body);
  assert.deepEqual(body, { username: "wiebe", password: "right" });
});

test("logout follows the end-session URL the server returned", async () => {
  routes["POST /api/v1/logout"] = { logout_url: "https://idp.test/logout?id_token_hint=x" };
  click(id("bb-logout"));
  await settleAll();
  assert.deepEqual(navigations.at(-1), "https://idp.test/logout?id_token_hint=x");
  assert.equal(core.state.authenticated, false);
  assert.equal(core.state.username, "");
  assert.deepEqual(tracked.at(-1), ["logout", { username: "wiebe" }]);
});

test("logout of an OIDC session without a server URL asks the runtime config for one", async () => {
  win.document.cookie = "bugbarn_auth_method=oidc";
  routes["POST /api/v1/logout"] = {};
  routes["GET /api/v1/runtime-config"] = { oidc: { endSessionURL: "https://idp.test/end" } };
  await http.logout();
  assert.equal(navigations.at(-1), "https://idp.test/end");
  win.document.cookie = "bugbarn_auth_method=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
});

test("a plain logout returns to the login form", async () => {
  routes["POST /api/v1/logout"] = { status: 500, body: {} };
  routes["GET /api/v1/runtime-config"] = { oidc: {} };
  const before = navigations.length;
  await http.logout();
  assert.equal(navigations.length, before);
  assert.equal(id("login-screen").hidden, false);
});

test("the login screen starts the OIDC redirect once when the runtime config enables it", async () => {
  routes["GET /api/v1/runtime-config"] = { oidc: { enabled: true, loginURL: "/api/v1/oidc/login" } };
  http.renderLogin();
  await settleAll();
  http.renderLogin();
  await settleAll();
  assert.equal(navigations.filter((u) => u === "/api/v1/oidc/login").length, 1);
});

test("an oidc_error in the URL renders the access-denied card with switch and sign-out links", async () => {
  routes["GET /api/v1/runtime-config"] = {
    oidc: { loginURL: "/login-again", switchAccountURL: "/switch", endSessionURL: "https://idp.test/end?x=1" },
  };
  win.history.replaceState(null, "", "/?oidc_error=denied&identity=bob%40example.com#/issues");
  http.renderLogin();
  await settleAll();
  const text = id("login-error").textContent;
  assert.match(text, /signed in to IAMBarn as bob@example\.com/);
  const links = [...id("login-error").querySelectorAll("a")].map((a) => [a.textContent, a.getAttribute("href")]);
  assert.deepEqual(links[0], ["Switch account", "/switch"]);
  assert.equal(links[1][0], "Sign out of IAMBarn");
  assert.match(links[1][1], /^https:\/\/idp\.test\/end\?x=1&post_logout_redirect_uri=/);
  assert.equal(win.location.search, "", "the query string is stripped");
});

test("fetchJson sends the group header, then the project header, and none when unscoped", async () => {
  routes["GET /api/v1/probe"] = { ok: true };
  core.state.currentGroup = "frontend";
  await http.fetchJson("/api/v1/probe");
  core.state.currentGroup = null;
  core.state.currentProject = "web";
  await http.fetchJson("/api/v1/probe");
  await http.fetchJson("/api/v1/probe", false, true);
  const headers = callsTo("GET", "/api/v1/probe").map((c) => [c.init.headers.get("X-BugBarn-Group"), c.init.headers.get("X-BugBarn-Project")]);
  assert.deepEqual(headers, [["frontend", null], [null, "web"], [null, null]]);
});

test("fetchJson shares one request between concurrent callers", async () => {
  routes["GET /api/v1/shared"] = { n: 1 };
  const before = callsTo("GET", "/api/v1/shared").length;
  const [a, b] = await Promise.all([http.fetchJson("/api/v1/shared"), http.fetchJson("/api/v1/shared")]);
  assert.deepEqual(a, b);
  assert.equal(callsTo("GET", "/api/v1/shared").length - before, 1);
});

test("fetchJson maps 404 to null when allowed, throws otherwise, and tolerates empty and plain bodies", async () => {
  routes["GET /api/v1/missing"] = { status: 404, body: {} };
  assert.equal(await http.fetchJson("/api/v1/missing", true), null);
  await assert.rejects(http.fetchJson("/api/v1/missing"), /404/);
  routes["GET /api/v1/empty"] = { status: 200, body: "" };
  assert.equal(await http.fetchJson("/api/v1/empty"), null);
  routes["GET /api/v1/plain"] = { status: 200, body: "not json" };
  assert.equal(await http.fetchJson("/api/v1/plain"), "not json");
});

test("fetchJson on a 401 flips the session to unauthenticated and shows the login", async () => {
  routes["GET /api/v1/private"] = { status: 401, body: {} };
  core.state.authenticated = true;
  await assert.rejects(http.fetchJson("/api/v1/private"));
  assert.equal(core.state.authenticated, false);
  assert.equal(id("login-screen").hidden, false);
});

test("postJson sends the CSRF cookie as a header and parses the reply", async () => {
  win.document.cookie = "bugbarn_csrf=tok%201";
  routes["POST /api/v1/thing"] = { done: true };
  assert.deepEqual(await http.postJson("/api/v1/thing", { a: 1 }), { done: true });
  const call = callsTo("POST", "/api/v1/thing").at(-1);
  assert.equal(call.init.headers.get("X-BugBarn-CSRF"), "tok 1");
  assert.equal(call.init.body, '{"a":1}');
});

test("postJson refreshes the CSRF token and retries once after a CSRF rejection", async () => {
  let attempts = 0;
  routes["POST /api/v1/csrf"] = () => (++attempts === 1 ? { status: 403, body: "CSRF token mismatch" } : { retried: true });
  assert.deepEqual(await http.postJson("/api/v1/csrf", {}), { retried: true });
  assert.equal(attempts, 2);
});

test("postJson reports other failures with the status and the start of the body", async () => {
  routes["POST /api/v1/forbidden"] = { status: 403, body: "no way" };
  await assert.rejects(http.postJson("/api/v1/forbidden", {}), /403.*no way/);
  routes["POST /api/v1/broken"] = { status: 500, body: "x".repeat(300) };
  await assert.rejects(http.postJson("/api/v1/broken", {}), (err) => err.message.length < 260 && err.message.startsWith("500"));
  routes["POST /api/v1/nobody"] = { status: 200, body: "" };
  assert.equal(await http.postJson("/api/v1/nobody", {}), null);
});

test("postJson on a 401 shows the login", async () => {
  routes["POST /api/v1/expired"] = { status: 401, body: "" };
  await assert.rejects(http.postJson("/api/v1/expired", {}));
  assert.equal(core.state.authenticated, false);
});

test("deleteJson sends DELETE and throws on failure", async () => {
  routes["DELETE /api/v1/thing"] = { deleted: 1 };
  assert.deepEqual(await http.deleteJson("/api/v1/thing"), { deleted: 1 });
  routes["DELETE /api/v1/locked"] = { status: 409, body: "in use" };
  await assert.rejects(http.deleteJson("/api/v1/locked"), /409.*in use/);
  routes["DELETE /api/v1/blank"] = { status: 200, body: "" };
  assert.equal(await http.deleteJson("/api/v1/blank"), null);
});

test("postFormData posts the form without a JSON content type", async () => {
  routes["POST /api/v1/upload"] = { ok: 1 };
  const form = new win.FormData();
  form.set("file", "x");
  assert.deepEqual(await http.postFormData("/api/v1/upload", form), { ok: 1 });
  routes["POST /api/v1/up-bad"] = { status: 413, body: "" };
  await assert.rejects(http.postFormData("/api/v1/up-bad", form), /413/);
  routes["POST /api/v1/up-401"] = { status: 401, body: "" };
  await assert.rejects(http.postFormData("/api/v1/up-401", form));
});

test("apiFetch retries a CSRF rejection and returns other responses untouched", async () => {
  let attempts = 0;
  routes["PATCH /api/v1/p"] = () => (++attempts === 1 ? { status: 403, body: "CSRF" } : { fine: true });
  const res = await http.apiFetch("/api/v1/p", { method: "PATCH" });
  assert.equal(res.status, 200);
  assert.equal(attempts, 2);
  routes["PATCH /api/v1/q"] = { status: 403, body: "denied" };
  assert.equal((await http.apiFetch("/api/v1/q", { method: "PATCH" })).status, 403);
});

test("loadSession records an authenticated session, a server error and a network failure", async () => {
  routes["GET /api/v1/me"] = { authEnabled: false, authenticated: true, username: "solo" };
  await http.loadSession();
  assert.deepEqual([core.state.authRequired, core.state.authenticated, core.state.username], [false, true, "solo"]);
  routes["GET /api/v1/me"] = { status: 500, body: {} };
  await http.loadSession();
  assert.deepEqual([core.state.authRequired, core.state.authenticated], [false, true]);
  routes["GET /api/v1/me"] = () => {
    throw new Error("down");
  };
  core.state.authenticated = false;
  await http.loadSession();
  assert.equal(core.state.authenticated, true);
});
