// Project picker, scope sheet, environment switcher and the shell listeners
// that bootstrap registers (sidebar, avatar menu, mobile nav, refresh).
import test from "node:test";
import assert from "node:assert/strict";
import { boot, go, q, qa, click, type, pick, settleAll, win } from "./support/app-harness.mjs";

const fetchStub = await boot();
const id = (name) => win.document.getElementById(name);
const key = (el, k) => el.dispatchEvent(new win.KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
const issueCalls = () => fetchStub.calls.filter((c) => c.url.startsWith("/api/v1/issues?"));
const lastIssueCall = () => issueCalls().at(-1);
const header = (call, name) => (call.init.headers instanceof win.Headers || typeof call.init.headers.get === "function" ? call.init.headers.get(name) : call.init.headers[name]);

test("the picker opens with every project and filters by name", async () => {
  click(id("project-picker-btn"));
  assert.equal(id("project-picker-dropdown").hidden, false);
  assert.deepEqual(qa("#project-picker-list [data-pick-project]").map((b) => b.dataset.pickProject), ["__all", "web", "api"]);
  type(id("project-picker-filter"), "AP");
  assert.deepEqual(qa("#project-picker-list [data-pick-project]").map((b) => b.dataset.pickProject), ["api"]);
  type(id("project-picker-filter"), "nomatch");
  assert.match(id("project-picker-list").textContent, /No matches/);
});

test("picking a project scopes issue requests with the project header and loads environments", async () => {
  type(id("project-picker-filter"), "");
  click(q("#project-picker-list [data-pick-project='web']"));
  await settleAll();
  assert.equal(id("project-picker-dropdown").hidden, true);
  assert.equal(win.localStorage.getItem("bugbarn_project"), "web");
  assert.match(id("project-picker-btn").textContent, /Web/);
  assert.equal(header(lastIssueCall(), "X-BugBarn-Project"), "web");
  assert.ok(fetchStub.calls.some((c) => c.url.startsWith("/api/v1/facets/attributes.environment")));
  assert.deepEqual(qa("#env-select option").map((o) => o.value), ["", "production", "staging"]);
});

test("choosing an environment adds it to the issue query and the scope label", async () => {
  const select = id("env-select");
  pick(select, "production");
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_env"), "production");
  assert.match(lastIssueCall().url, /attributes\.environment=production/);
});

test("keyboard navigation moves through picker items and Escape closes it", () => {
  click(id("project-picker-btn"));
  key(id("project-picker-filter"), "ArrowDown");
  assert.ok(win.document.activeElement.classList.contains("picker-item"));
  key(id("project-picker-list"), "ArrowDown");
  key(id("project-picker-list"), "ArrowUp");
  key(id("project-picker-list"), "ArrowUp");
  assert.equal(win.document.activeElement, id("project-picker-filter"));
  key(id("project-picker-filter"), "Escape");
  assert.equal(id("project-picker-dropdown").hidden, true);
  click(id("project-picker-btn"));
  key(id("project-picker-list"), "Escape");
  assert.equal(id("project-picker-dropdown").hidden, true);
});

test("clicking the button twice closes the picker and an outside click closes it too", () => {
  click(id("project-picker-btn"));
  click(id("project-picker-btn"));
  assert.equal(id("project-picker-dropdown").hidden, true);
  click(id("project-picker-btn"));
  click(win.document.body);
  assert.equal(id("project-picker-dropdown").hidden, true);
});

test("groups appear in the picker once settings loaded them, and picking one sets the group header", async () => {
  await go("#/settings");
  click(id("project-picker-btn"));
  const group = q("#project-picker-list [data-pick-group='frontend']");
  assert.ok(group, "group row");
  assert.match(group.textContent, /Frontend/);
  click(group);
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_group"), "frontend");
  assert.equal(win.localStorage.getItem("bugbarn_project"), null);
  assert.match(id("project-picker-btn").textContent, /Frontend/);
  await go("#/issues");
  assert.equal(header(lastIssueCall(), "X-BugBarn-Group"), "frontend");
});

test("the scope sheet lists groups and projects, filters, and selects a project", async () => {
  click(id("scope-btn"));
  assert.equal(id("scope-sheet").hidden, false);
  assert.ok(q("#scope-sheet-body [data-scope-group='frontend']"));
  type(q("#scope-sheet-body .scope-sheet-filter"), "api");
  assert.deepEqual(qa("#scope-sheet-body [data-scope-project]").map((b) => b.dataset.scopeProject), ["api"]);
  click(q("#scope-sheet-body [data-scope-project='api']"));
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_project"), "api");
  assert.equal(win.localStorage.getItem("bugbarn_group"), null);
  assert.ok(q("#scope-sheet-body [data-scope-env='']"), "the project scope offers the environment section");
});

test("All environments in the scope sheet clears the environment, closes the sheet and reloads", async () => {
  win.localStorage.setItem("bugbarn_env", "staging");
  click(q("#scope-sheet-body [data-scope-env='']"));
  await settleAll();
  assert.equal(id("scope-sheet").hidden, true);
  assert.equal(win.localStorage.getItem("bugbarn_env"), null);
  assert.doesNotMatch(lastIssueCall().url, /attributes\.environment/);
});

test("the scope sheet group and all-projects buttons reset the scope", async () => {
  click(id("scope-btn"));
  click(q("#scope-sheet-body [data-scope-group='frontend']"));
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_group"), "frontend");
  click(id("scope-btn"));
  click(q("#scope-sheet-body [data-scope-project='__all']"));
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_group"), null);
  assert.equal(win.localStorage.getItem("bugbarn_project"), "__all");
  assert.equal(id("scope-sheet").hidden, true);
});

test("the scope sheet closes from its close button and its backdrop", () => {
  click(id("scope-btn"));
  click(id("scope-close"));
  assert.equal(id("scope-sheet").hidden, true);
  click(id("scope-btn"));
  click(id("scope-backdrop"));
  assert.equal(id("scope-backdrop").hidden, true);
});

test("a project event from the timeline legend selects that project", async () => {
  win.document.dispatchEvent(new win.CustomEvent("bugbarn:select-project", { detail: "web" }));
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn_project"), "web");
  win.document.dispatchEvent(new win.CustomEvent("bugbarn:select-project", { detail: "" }));
  assert.equal(win.localStorage.getItem("bugbarn_project"), "web");
});

test("the pending banner names the projects awaiting approval", async () => {
  const stub = fetchStub;
  assert.ok(stub.calls.some((c) => c.url === "/api/v1/projects/pending-count"));
  assert.equal(id("pending-banner").hidden, false);
  assert.match(id("pending-banner").textContent, /1 project awaiting approval/);
  assert.equal(q(".side-nav a[data-route='settings'] .nav-badge").textContent, "1");
});

test("the sidebar toggle flips and stores its state", () => {
  const toggle = id("sidebar-toggle");
  const before = win.localStorage.getItem("bugbarn_sidebar");
  click(toggle);
  const after = win.localStorage.getItem("bugbarn_sidebar");
  assert.notEqual(before, after);
  assert.equal(toggle.getAttribute("aria-label"), after === "expanded" ? "Collapse sidebar" : "Expand sidebar");
  click(toggle);
  assert.equal(win.localStorage.getItem("bugbarn_sidebar"), before === "expanded" ? "expanded" : "collapsed");
});

test("the avatar menu opens, then closes on Escape and on an outside click", () => {
  const avatar = id("user-avatar-btn");
  click(avatar);
  assert.equal(id("bb-menu").hasAttribute("hidden"), false);
  assert.equal(avatar.getAttribute("aria-expanded"), "true");
  key(win.document, "Escape");
  assert.equal(id("bb-menu").hasAttribute("hidden"), true);
  click(avatar);
  click(win.document.body);
  assert.equal(id("bb-menu").hasAttribute("hidden"), true);
  click(avatar);
  click(avatar);
  assert.equal(avatar.getAttribute("aria-expanded"), "false");
});

test("the mobile menu button opens and closes the navigation, and a nav link closes it", () => {
  const frame = q(".app-frame");
  const button = id("mobile-menu-btn");
  click(button);
  assert.ok(frame.classList.contains("mobile-nav-open"));
  assert.equal(button.getAttribute("aria-expanded"), "true");
  click(button);
  assert.ok(!frame.classList.contains("mobile-nav-open"));
  click(button);
  click(q(".side-nav a"));
  assert.ok(!frame.classList.contains("mobile-nav-open"));
});

test("the refresh button reloads the issues", async () => {
  const before = issueCalls().length;
  click(id("refresh-all"));
  await settleAll();
  assert.ok(issueCalls().length > before);
});

test("the user menu shows the signed-in name", () => {
  assert.equal(id("user-avatar-initial").textContent, "W");
  assert.equal(id("bb-menu-user").textContent, "wiebe");
});
