// The releases view: list, environment filter, release detail and the marker form.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, settleAll, text, win } from "./support/app-harness.mjs";

const ago = (m) => new Date(Date.now() - m * 60000).toISOString();
const releases = [
  { id: "r2", name: "2.0.0", environment: "staging", observedAt: ago(30), version: "2.0.0", commitSha: "bbb2222" },
  { id: "r1", name: "1.0.0", environment: "production", observedAt: ago(300), version: "1.0.0", commitSha: "aaa1111", url: "https://ci.test/1", notes: "first" },
];
const tracked = [];
win.funnelbarn = { init() {}, page() {}, track: (n, p) => tracked.push([n, p]) };
const routes = defaultRoutes({
  "GET /api/v1/releases": { releases },
  "GET /api/v1/issues": ({ url }) => (url.includes("new_in_release=r1")
    ? { issues: [{ id: "n1", title: "Introduced in 1.0.0", status: "unresolved", event_count: 2 }] }
    : { issues: [{ id: "g1", title: "Regressed later", status: "regressed", event_count: 3, LastRegressedAt: ago(100), first_seen: ago(400) }] }),
});
const fetchStub = await boot(routes, "#/releases");

test("the list shows each release with its environment", () => {
  const cards = qa("[data-release-id]");
  assert.deepEqual(cards.map((c) => c.dataset.releaseId), ["r2", "r1"]);
  assert.match(cards[0].textContent, /2\.0\.0/);
  assert.match(text("#overview-view"), /staging/);
});

test("the environment buttons filter the cards", () => {
  const prod = qa(".env-filter-btn").find((b) => b.dataset.env === "production");
  click(prod);
  assert.deepEqual(qa("[data-release-id]").map((c) => c.dataset.releaseId), ["r1"]);
  click(qa(".env-filter-btn").find((b) => b.dataset.env === ""));
  assert.equal(qa("[data-release-id]").length, 2);
});

test("a card opens by click and by Enter or Space", () => {
  click(q("[data-release-id='r1']"));
  assert.equal(win.location.hash, "#/releases/r1");
  win.location.hash = "#/releases";
  q("[data-release-id='r2']").dispatchEvent(new win.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  assert.equal(win.location.hash, "#/releases/r2");
  win.location.hash = "#/releases";
  q("[data-release-id='r2']").dispatchEvent(new win.KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true }));
  assert.equal(win.location.hash, "#/releases/r2");
});

test("the detail asks the server for the issues new in that release and lists them", async () => {
  await go("#/releases/r1");
  assert.match(text("#detail-title"), /1\.0\.0/);
  assert.ok(fetchStub.calls.some((c) => c.url.includes("new_in_release=r1") && c.url.includes("status=all")));
  assert.match(text("#detail-body"), /Introduced in 1\.0\.0/);
  click(q("#detail-body [data-issue-id]"));
  assert.equal(win.location.hash, "#/issues/n1");
});

test("when the server query fails the detail falls back to issues first seen in the window", async () => {
  routes["GET /api/v1/issues"] = ({ url }) => (url.includes("new_in_release")
    ? { status: 500, body: {} }
    : { issues: [{ id: "f1", title: "Seen inside window", status: "unresolved", first_seen: ago(200), event_count: 1 }, { id: "f2", title: "Seen before", status: "unresolved", first_seen: ago(900), event_count: 1 }, { id: "f3", title: "Regressed here", status: "regressed", first_seen: ago(900), LastRegressedAt: ago(100), event_count: 1 }] });
  await go("#/alerts");
  await go("#/releases/r1");
  const detail = text("#detail-body");
  assert.match(detail, /Seen inside window/);
  assert.doesNotMatch(detail, /Seen before/);
  assert.match(detail, /Regressed here/);
});

test("the detail of the newest release is bounded by now, and an unknown release says so", async () => {
  await go("#/releases/r2");
  assert.match(text("#detail-title"), /2\.0\.0/);
  await go("#/releases/ghost");
  assert.match(text("#detail-title"), /Release not found/);
  assert.match(text("#detail-body"), /Release ghost not found/);
});

test("a failed release list shows the error", async () => {
  routes["GET /api/v1/releases"] = { status: 500, body: "releases offline" };
  await go("#/alerts");
  await go("#/releases");
  assert.match(text("#overview-view"), /Unable to load releases/);
  routes["GET /api/v1/releases"] = { releases };
});

test("saving a release marker posts the form, tracks it and reloads the list", async () => {
  await go("#/alerts");
  await go("#/releases");
  const form = q("#release-form");
  form.querySelector("[name=name]").value = "3.0.0";
  const env = form.querySelector("[name=environment]");
  if (env) env.value = "production";
  routes["POST /api/v1/releases"] = { ok: true };
  form.dispatchEvent(new win.Event("submit", { bubbles: true, cancelable: true }));
  await settleAll();
  const post = fetchStub.calls.filter((c) => c.method === "POST" && c.url === "/api/v1/releases").at(-1);
  assert.equal(JSON.parse(post.init.body).name, "3.0.0");
  assert.deepEqual(tracked.at(-1), ["release_created", { name: "3.0.0", environment: env ? "production" : "" }]);
  assert.match(text("#status-text"), /Release marker saved/);
});

test("a rejected marker reports why", async () => {
  routes["POST /api/v1/releases"] = { status: 400, body: "name required" };
  q("#release-form").dispatchEvent(new win.Event("submit", { bubbles: true, cancelable: true }));
  await settleAll();
  assert.match(text("#status-text"), /Release marker unavailable: .*name required/);
});
