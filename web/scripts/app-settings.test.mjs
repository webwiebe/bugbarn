// Settings: preferences, source maps, projects, groups, aliases, key requests and the system tab.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, click, settleAll, text, win } from "./support/app-harness.mjs";

const tracked = [];
win.funnelbarn = { init() {}, page() {}, track: (n, p) => tracked.push([n, p]) };
const routes = defaultRoutes({
  "GET /packages/typescript/latest.json": { version: "1.2.3", filename: "x.tgz", url: "/packages/typescript/x.tgz" },
});
const fetchStub = await boot(routes, "#/settings");
const tabs = ["#/settings", "#/settings/projects", "#/settings/preferences", "#/settings/keys", "#/settings/system", "#/settings/volume"];
const callsTo = (method, part) => fetchStub.calls.filter((c) => c.method === method && c.url.includes(part));
const submit = (form) => form.dispatchEvent(new win.Event("submit", { bubbles: true, cancelable: true }));
const flash = () => text("body");

/** Opens the settings tab that renders the selector. */
async function open(selector) {
  for (const hash of tabs) {
    await go(hash);
    if (q(selector)) return q(selector);
  }
  assert.fail(`no settings tab renders ${selector}`);
}

test("saving preferences posts the form values and confirms", async () => {
  const form = await open("#settings-form");
  form.querySelector("[name=displayName]").value = "Ada";
  submit(form);
  await settleAll();
  const body = JSON.parse(callsTo("POST", "/api/v1/settings").at(-1).init.body);
  assert.equal(body.displayName, "Ada");
  assert.equal(typeof body.liveWindowMinutes, "number");
  assert.equal(typeof body.stacktraceContextLines, "number");
  assert.match(text("#status-text"), /Settings saved/);
});

test("a failing preferences save reports it", async () => {
  routes["POST /api/v1/settings"] = { status: 500, body: "disk full" };
  submit(await open("#settings-form"));
  await settleAll();
  assert.match(text("#status-text"), /Settings unavailable: .*disk full/);
  delete routes["POST /api/v1/settings"];
});

test("uploading source maps posts the form data, and a failure is reported", async () => {
  const form = await open("#source-map-form");
  submit(form);
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/source-maps").length, 1);
  assert.match(text("#status-text"), /Source maps uploaded/);
  assert.ok(tracked.some((t) => t[0] === "sourcemaps_uploaded"));
  routes["POST /api/v1/source-maps"] = { status: 413, body: "" };
  submit(form);
  await settleAll();
  assert.match(text("#status-text"), /Source map upload unavailable/);
  delete routes["POST /api/v1/source-maps"];
});

test("the setup URL copy button copies it and shows a check mark", async () => {
  const btn = await open("#copy-setup-url");
  click(btn);
  assert.equal(btn.textContent, "✓");
  assert.ok(win.navigator.clipboard.written.length > 0);
});

test("the SDK info block loads the latest package, or says it is not published", async () => {
  await open("#sdk-info");
  const { loadSdkInfo } = await import("../src/views-settings.ts");
  await loadSdkInfo();
  assert.match(text("#sdk-info"), /1\.2\.3/);
  assert.match(text("#sdk-info"), /pnpm add https:\/\/bugbarn\.test\/packages\/typescript\/x\.tgz/);
  routes["GET /packages/typescript/latest.json"] = { status: 404, body: {} };
  await loadSdkInfo();
  assert.match(text("#sdk-info"), /Package not yet published/);
});

test("approving a pending project posts the approval and reloads the lists", async () => {
  const btn = await open("[data-approve-project]");
  const slug = btn.dataset.approveProject;
  click(btn);
  await settleAll();
  assert.equal(callsTo("POST", `/api/v1/projects/${slug}/approve`).length, 1);
  assert.match(flash(), /approved/);
  assert.ok(tracked.some((t) => t[0] === "project_approved"));
});

test("a failed approval re-enables the button and flashes the error", async () => {
  routes["POST /api/v1/projects/api/approve"] = { status: 500, body: "approval blocked" };
  const btn = await open("[data-approve-project]");
  click(btn);
  await settleAll();
  assert.match(flash(), /Approve failed: .*approval blocked/);
  assert.equal(q("[data-approve-project]").disabled, false);
  delete routes["POST /api/v1/projects/api/approve"];
});

test("key requests can be approved or denied, and failures are flashed", async () => {
  const btn = await open("[data-approve-key-request]");
  click(btn);
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/key-requests/kr1/approve").length, 1);
  assert.match(flash(), /Key request approved/);
  click(q("[data-deny-key-request]"));
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/key-requests/kr1/deny").length, 1);
  assert.match(flash(), /Key request denied/);
  routes["POST /api/v1/key-requests/kr1/approve"] = { status: 500, body: "nope" };
  click(q("[data-approve-key-request]"));
  await settleAll();
  assert.match(flash(), /Key request approve failed: .*nope/);
  delete routes["POST /api/v1/key-requests/kr1/approve"];
});

test("the FunnelBarn link form saves a URL, clears it, and reports failures", async () => {
  const form = await open("[data-funnelbarn-form]");
  form.querySelector("input[name=funnelbarn_url]").value = " https://fb.test/p ";
  submit(form);
  await settleAll();
  const put = callsTo("PUT", "/funnelbarn").at(-1);
  assert.deepEqual(JSON.parse(put.init.body), { funnelbarn_url: "https://fb.test/p" });
  assert.match(flash(), /FunnelBarn link saved for/);
  q("[data-funnelbarn-form] input[name=funnelbarn_url]").value = "";
  submit(q("[data-funnelbarn-form]"));
  await settleAll();
  assert.match(flash(), /FunnelBarn link cleared for/);
  routes["PUT /api/v1/projects/"] = { status: 400, body: "bad url" };
  submit(q("[data-funnelbarn-form]"));
  await settleAll();
  assert.match(flash(), /Saving the FunnelBarn link failed: .*bad url/);
  delete routes["PUT /api/v1/projects/"];
});

test("deleting a project asks for confirmation, then deletes it", async () => {
  const btn = await open("[data-delete-project]");
  win.confirm = () => false;
  globalThis.confirm = win.confirm;
  click(btn);
  await settleAll();
  assert.equal(callsTo("DELETE", "/api/v1/projects/").length, 0);
  win.confirm = () => true;
  globalThis.confirm = win.confirm;
  click(q("[data-delete-project]"));
  await settleAll();
  assert.equal(callsTo("DELETE", "/api/v1/projects/").length, 1);
  assert.match(flash(), /deleted/);
  routes["DELETE /api/v1/projects/"] = { status: 409, body: { error: "project has keys" } };
  click(q("[data-delete-project]"));
  await settleAll();
  assert.match(flash(), /Delete failed: project has keys/);
  delete routes["DELETE /api/v1/projects/"];
});

const withUngrouped = () => {
  routes["GET /api/v1/projects"] = () => ({ projects: [
    { id: 1, slug: "web", name: "Web", status: "active", group_id: 1, issue_count: 1, event_count: 1, log_count: 0 },
    { id: 3, slug: "docs", name: "Docs", status: "active", group_id: null, issue_count: 0, event_count: 0, log_count: 0 },
  ] });
};

test("groups can be created, filled, emptied and deleted", async () => {
  withUngrouped();
  const form = await open("#create-group-form");
  form.querySelector("[name=name]").value = "Backend";
  submit(form);
  await settleAll();
  assert.deepEqual(JSON.parse(callsTo("POST", "/api/v1/groups").at(-1).init.body), { name: "Backend" });
  assert.ok(tracked.some((t) => t[0] === "project_group_created"));
  submit(q("#create-group-form"));
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/groups").length, 1, "an empty name posts nothing");

  const add = q("[data-add-to-group]");
  const select = add.querySelector("select");
  select.value = select.options[select.options.length - 1].value;
  submit(add);
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/groups/frontend/projects").length, 1);

  click(q("[data-remove-from-group]"));
  await settleAll();
  assert.equal(callsTo("DELETE", "/api/v1/groups/frontend/projects/").length, 1);

  click(q("[data-delete-group]"));
  await settleAll();
  assert.equal(callsTo("DELETE", "/api/v1/groups/frontend").length >= 1, true);
});

test("group failures are flashed", async () => {
  withUngrouped();
  await open("#create-group-form");
  routes["POST /api/v1/groups"] = { status: 500, body: "group store down" };
  q("#create-group-form [name=name]").value = "X";
  submit(q("#create-group-form"));
  await settleAll();
  assert.match(flash(), /Create group failed/);
  routes["DELETE /api/v1/groups/"] = { status: 500, body: "" };
  click(q("[data-delete-group]"));
  await settleAll();
  assert.match(flash(), /Delete group failed/);
  routes["POST /api/v1/groups/frontend/projects"] = { status: 500, body: "" };
  submit(q("[data-add-to-group]"));
  await settleAll();
  assert.match(flash(), /Add to group failed/);
  delete routes["POST /api/v1/groups"];
  delete routes["DELETE /api/v1/groups/"];
  delete routes["POST /api/v1/groups/frontend/projects"];
});

test("aliases can be created and deleted, and failures are flashed", async () => {
  const form = await open("#create-alias-form");
  form.querySelector("[name=alias]").value = "marketing";
  form.querySelector("[name=project]").value = "web";
  submit(form);
  await settleAll();
  assert.deepEqual(JSON.parse(callsTo("POST", "/api/v1/aliases").at(-1).init.body), { alias: "marketing", project: "web" });
  assert.ok(tracked.some((t) => t[0] === "project_alias_created"));
  click(q("[data-delete-alias]"));
  await settleAll();
  assert.equal(callsTo("DELETE", "/api/v1/aliases/site").length, 1);
  assert.match(flash(), /Alias "site" deleted/);
  routes["DELETE /api/v1/aliases/"] = { status: 500, body: "" };
  click(q("[data-delete-alias]"));
  await settleAll();
  assert.match(flash(), /Delete alias failed/);
  routes["POST /api/v1/aliases"] = { status: 500, body: "dupe" };
  q("#create-alias-form [name=alias]").value = "m2";
  q("#create-alias-form [name=project]").value = "web";
  submit(q("#create-alias-form"));
  await settleAll();
  assert.match(flash(), /Create alias failed/);
});

test("the settings logout button signs out", async () => {
  routes["POST /api/v1/logout"] = {};
  click(await open("#settings-logout"));
  await settleAll();
  assert.equal(callsTo("POST", "/api/v1/logout").length, 1);
});

test("the system tab shows the health snapshot and survives a failing health call", async () => {
  await go("#/settings/system");
  assert.ok(callsTo("GET", "/api/v1/health").length >= 1);
  routes["GET /api/v1/health"] = { status: 500, body: "" };
  await go("#/alerts");
  await go("#/settings/system");
  assert.ok(text("#overview-view").length > 0);
});

test("a settings load failure renders the error state", async () => {
  routes["GET /api/v1/settings"] = { status: 500, body: "settings backend down" };
  await go("#/alerts");
  await go("#/settings");
  assert.ok(callsTo("GET", "/api/v1/settings").length > 1, "the load was attempted");
  assert.match(text("#overview-view"), /Settings/, "the view still renders");
});
