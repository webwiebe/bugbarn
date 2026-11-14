// Issue and event detail: actions, paging older events, copy buttons and error states.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, pick, settleAll, text, win } from "./support/app-harness.mjs";

const ago = (m) => new Date(Date.now() - m * 60000).toISOString();
const ev = (n) => ({ id: `event-${n}`, issueId: "issue-1", title: `Event ${n}`, timestamp: ago(n), severityText: "error" });
const tracked = [];
win.funnelbarn = { init() {}, page() {}, track: (n, p) => tracked.push([n, p]) };
let issueStatus = "unresolved";
const routes = defaultRoutes({
  "GET /api/v1/issues/issue-1/events": ({ url }) => (url.includes("before=") ? { events: [ev(30), ev(31)], hasMore: false } : { events: [ev(1), ev(2)], hasMore: true }),
  "GET /api/v1/issues/issue-1": () => ({ issue: { id: "issue-1", title: "TypeError number 1", status: issueStatus, event_count: 9, first_release: "1.0.0", first_seen: ago(500), last_seen: ago(5), exceptionType: "TypeError" } }),
});
const fetchStub = await boot(routes, "#/issues/issue-1");
const callsTo = (method, part) => fetchStub.calls.filter((c) => c.method === method && c.url.includes(part));
const flash = () => text("body");

test("the issue detail shows the title, status and the first page of events", () => {
  assert.match(text("#detail-title"), /TypeError number 1/);
  assert.equal(qa("#detail-body [data-event-id]").length, 2);
  assert.ok(q("#detail-body [data-load-older]"));
  assert.deepEqual(tracked.find((t) => t[0] === "issue_viewed"), ["issue_viewed", { issueId: "issue-1" }]);
});

test("an event row opens the event", () => {
  click(q("#detail-body [data-event-id]"));
  assert.match(win.location.hash, /^#\/events\/event-/);
});

test("Load older events fetches before the oldest row id and merges the page", async () => {
  await go("#/issues/issue-1");
  click(q("#detail-body [data-load-older]"));
  await settleAll();
  assert.ok(callsTo("GET", "/issues/issue-1/events?limit=25&before=").length >= 1);
  assert.equal(qa("#detail-body [data-event-id]").length, 4);
  assert.equal(q("#detail-body [data-load-older]"), null, "no further pages");
});

test("a failed older page re-enables the button", async () => {
  routes["GET /api/v1/issues/issue-1/events"] = ({ url }) => (url.includes("before=") ? { status: 500, body: {} } : { events: [ev(1)], hasMore: true });
  await go("#/alerts");
  await go("#/issues/issue-1");
  const btn = q("#detail-body [data-load-older]");
  click(btn);
  await settleAll();
  assert.equal(q("#detail-body [data-load-older]").disabled, false);
});

test("copy writes the id to the clipboard and reports it, and a clipboard failure is reported too", async () => {
  click(q("#detail-body [data-copy-id]"));
  await settleAll();
  assert.deepEqual(win.navigator.clipboard.written.at(-1), "issue-1");
  assert.match(text("#status-text"), /Copied issue-1/);
  const original = win.navigator.clipboard.writeText;
  win.navigator.clipboard.writeText = async () => {
    throw new Error("denied");
  };
  click(q("#detail-body [data-copy-id]"));
  await settleAll();
  assert.match(text("#status-text"), /Could not copy issue-1/);
  win.navigator.clipboard.writeText = original;
});

test("Resolve posts to the issue, flashes the result and reloads the detail", async () => {
  click(q("#detail-body [data-resolve-issue]"));
  await settleAll();
  assert.ok(callsTo("POST", "/api/v1/issues/issue-1/resolve").length === 1);
  assert.match(flash(), /Issue issue-1 marked resolved/);
  assert.ok(tracked.some((t) => t[0] === "issue_resolved"));
});

test("a resolved issue offers Reopen", async () => {
  issueStatus = "resolved";
  await go("#/alerts");
  await go("#/issues/issue-1");
  click(q("#detail-body [data-reopen-issue]"));
  await settleAll();
  assert.ok(callsTo("POST", "/api/v1/issues/issue-1/reopen").length === 1);
  assert.ok(tracked.some((t) => t[0] === "issue_reopened"));
  issueStatus = "unresolved";
});

test("a failing resolve flashes the error", async () => {
  routes["POST /api/v1/issues/issue-1/resolve"] = { status: 500, body: "cannot resolve" };
  await go("#/alerts");
  await go("#/issues/issue-1");
  click(q("#detail-body [data-resolve-issue]"));
  await settleAll();
  assert.match(flash(), /Issue status change failed: .*cannot resolve/);
});

test("Mute sends the chosen mode and reloads", async () => {
  await go("#/alerts");
  await go("#/issues/issue-1");
  pick(q("#mute-mode-select"), "forever");
  click(q("[data-action='mute-issue']"));
  await settleAll();
  const call = callsTo("PATCH", "/api/v1/issues/issue-1/mute").at(-1);
  assert.deepEqual(JSON.parse(call.init.body), { mute_mode: "forever" });
  assert.ok(tracked.some((t) => t[0] === "issue_muted" && t[1].muteMode === "forever"));
});

test("a rejected mute shows the status", async () => {
  routes["PATCH /api/v1/issues/issue-1/mute"] = { status: 403, body: "" };
  await go("#/alerts");
  await go("#/issues/issue-1");
  click(q("[data-action='mute-issue']"));
  await settleAll();
  assert.match(flash(), /Mute failed: 403/);
  routes["PATCH /api/v1/issues/issue-1/mute"] = () => {
    throw new Error("socket closed");
  };
  click(q("[data-action='mute-issue']"));
  await settleAll();
  assert.match(flash(), /Mute failed: socket closed/);
  delete routes["PATCH /api/v1/issues/issue-1/mute"];
});

test("Unmute is offered for a muted issue and handles success and failure", async () => {
  routes["GET /api/v1/issues/issue-1"] = () => ({ issue: { id: "issue-1", title: "Muted one", status: "muted", mute_mode: "forever", event_count: 1 } });
  await go("#/alerts");
  await go("#/issues/issue-1");
  click(q("[data-action='unmute-issue']"));
  await settleAll();
  assert.ok(callsTo("PATCH", "/api/v1/issues/issue-1/unmute").length >= 1);
  assert.ok(tracked.some((t) => t[0] === "issue_unmuted"));
  routes["PATCH /api/v1/issues/issue-1/unmute"] = { status: 500, body: "" };
  click(q("[data-action='unmute-issue']"));
  await settleAll();
  assert.match(flash(), /Unmute failed: 500/);
  routes["PATCH /api/v1/issues/issue-1/unmute"] = () => {
    throw new Error("gone");
  };
  click(q("[data-action='unmute-issue']"));
  await settleAll();
  assert.match(flash(), /Unmute failed: gone/);
});

test("an issue that fails to load shows the error in the detail", async () => {
  routes["GET /api/v1/issues/issue-1"] = { status: 500, body: "issue store down" };
  await go("#/alerts");
  await go("#/issues/issue-1");
  assert.match(text("#detail-title"), /Issue issue-1/);
  assert.match(text("#detail-body"), /issue store down|500/);
  routes["GET /api/v1/issues/issue-1"] = () => ({ issue: { id: "issue-1", title: "TypeError number 1", status: "unresolved" } });
});

test("the event detail shows its issue, copies its id, and the issue button goes back", async () => {
  await go("#/events/event-1");
  assert.match(text("#detail-title"), /issue-1 — event-1/);
  click(q("#detail-body [data-copy-id]"));
  await settleAll();
  assert.equal(win.navigator.clipboard.written.at(-1), "event-1");
  click(q("#detail-body [data-open-issue]"));
  assert.equal(win.location.hash, "#/issues/issue-1");
});

test("an event row on the event detail switches events, and older events page in", async () => {
  routes["GET /api/v1/issues/issue-1/events"] = ({ url }) => (url.includes("before=") ? { events: [ev(30)], hasMore: false } : { events: [ev(1), ev(2)], hasMore: true });
  await go("#/alerts");
  await go("#/events/event-1");
  click(q("#detail-body [data-load-older]"));
  await settleAll();
  assert.ok(callsTo("GET", "before=").length >= 1);
  click(q("#detail-body [data-event-id]"));
  assert.match(win.location.hash, /^#\/events\//);
});

test("an event whose issue cannot be loaded still renders, and a missing event shows the error", async () => {
  routes["GET /api/v1/issues/issue-1"] = { status: 500, body: "" };
  await go("#/alerts");
  await go("#/events/event-1");
  assert.match(text("#detail-title"), /event-1/);
  routes["GET /api/v1/events/event-404"] = { status: 404, body: "" };
  await go("#/events/event-404");
  assert.match(text("#detail-title"), /Event event-404/);
});
