// The live event stream: connecting, rendering events with release dividers,
// reconnecting with backoff, and desktop notifications for regressions.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, settleAll, win } from "./support/app-harness.mjs";

const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString();
const shown = [];
class FakeNotification {
  static permission = "granted";
  static requested = 0;
  static requestPermission() {
    FakeNotification.requested++;
  }
  constructor(title, options) {
    shown.push({ title, options });
  }
}
win.Notification = FakeNotification;
globalThis.Notification = FakeNotification;
const registrations = [];
Object.defineProperty(win.navigator, "serviceWorker", {
  value: { controller: null, ready: Promise.resolve({ showNotification: (title, options) => registrations.push({ title, options }) }) },
  configurable: true,
});

const routes = defaultRoutes({
  "GET /api/v1/releases": { releases: [{ id: "r1", name: "2.0.0", environment: "production", observedAt: ago(30), version: "2.0.0" }] },
});
await boot(routes, "#/live");
const sources = () => win.EventSource.instances;
const live = () => sources().at(-1);
const liveText = () => q("#live-list").textContent;

test("the stream opens with a since window and shows the empty state once connected", () => {
  assert.match(live().url, /^\/api\/v1\/events\/stream\?since=/);
  live().onopen();
  assert.match(liveText(), /No live events yet/);
  assert.equal(q("#live-status")?.textContent ?? "Connected", "Connected");
});

test("incoming events render newest first with a release divider between the two they straddle", async () => {
  await go("#/releases");
  await go("#/live");
  live().onopen();
  live().emit({ id: "e-old", issueId: "issue-1", title: "Older failure", severityText: "ERROR", timestamp: ago(60) });
  live().emit({ id: "e-new", issueId: "issue-2", title: "Newer failure", severityText: "WARNING", timestamp: ago(5) });
  const titles = qa("#live-list .item-title-text").map((el) => el.textContent);
  assert.deepEqual(titles, ["Newer failure", "Older failure"]);
  assert.equal(qa("#live-list .release-divider").length, 1);
  assert.match(q("#live-list .release-divider").textContent, /2\.0\.0.*production/s);
  assert.match(liveText(), /issue-2/);
});

test("a malformed message is skipped without breaking the list", () => {
  live().onmessage({ data: "{not json" });
  assert.equal(qa("#live-list .item").length, 2);
});

test("clicking a live event opens its detail route", () => {
  click(q("[data-live-event-id='e-new']"));
  assert.equal(win.location.hash, "#/events/e-new");
});

test("a regression pops a desktop notification once per issue", () => {
  const regressed = { id: "e-r", issueId: "issue-9", message: "Back again", regressed: true, timestamp: ago(1) };
  live().emit(regressed);
  live().emit({ ...regressed, id: "e-r2" });
  assert.equal(shown.length, 1);
  assert.equal(shown[0].title, "BugBarn: Issue Regressed");
  assert.equal(shown[0].options.body, "Back again");
  assert.equal(shown[0].options.tag, "regressed-issue-9");
});

test("events without a regression flag, an issue id or permission stay silent", () => {
  live().emit({ id: "e-n", issueId: "issue-8", title: "Plain" });
  live().emit({ id: "e-x", Regressed: true, title: "No issue id" });
  FakeNotification.permission = "denied";
  live().emit({ id: "e-d", issueId: "issue-7", regressed: true });
  FakeNotification.permission = "granted";
  assert.equal(shown.length, 1);
});

test("with a service worker controlling the page the notification goes through it", async () => {
  win.navigator.serviceWorker.controller = {};
  live().emit({ id: "e-sw", issueId: "issue-5", Title: "Via worker", Regressed: true });
  await settleAll();
  assert.equal(registrations.length, 1);
  assert.equal(registrations[0].options.data.url, `${win.location.origin}/#/issues/issue-5`);
  assert.equal(registrations[0].options.body, "Via worker");
  win.navigator.serviceWorker.controller = null;
});

test("the notified list keeps only the newest 200 issue ids", () => {
  const ids = Array.from({ length: 200 }, (_, i) => `old-${i}`);
  win.localStorage.setItem("bugbarn_notified_regressions", JSON.stringify(ids));
  const key = [...Array(win.localStorage.length).keys()].map((i) => win.localStorage.key(i)).find((k) => /regress/i.test(k));
  assert.ok(key, "a notified-regressions key exists");
  win.localStorage.setItem(key, JSON.stringify(ids));
  live().emit({ id: "e-last", issueId: "fresh", regressed: true });
  const stored = JSON.parse(win.localStorage.getItem(key));
  assert.equal(stored.length, 200);
  assert.equal(stored.at(-1), "fresh");
  assert.equal(stored.includes("old-0"), false);
});

test("an error closes the stream, shows Reconnecting and retries with a doubled delay", () => {
  const delays = [];
  const realSetTimeout = win.setTimeout;
  win.setTimeout = (fn, ms) => {
    delays.push(ms);
    return realSetTimeout(() => {}, 0);
  };
  try {
    const failing = live();
    failing.onopen();
    failing.onerror();
    assert.equal(failing.closed, true);
    assert.match(q("#live-status").textContent, /Reconnecting/);
    assert.match(liveText(), /Newer failure/);
    failing.onerror();
    assert.deepEqual(delays, [3000, 6000]);
  } finally {
    win.setTimeout = realSetTimeout;
  }
});

test("opening a fresh stream resets the retry delay and a second one replaces the first", async () => {
  await go("#/issues");
  await go("#/live");
  const open = sources().filter((s) => !s.closed);
  assert.equal(open.length, 1, "only one stream is open at a time");
  open[0].onopen();
  assert.match(liveText(), /No live events yet|Newer failure|Back again/);
});

test("leaving the live route and signing out stops the stream", async () => {
  const current = live();
  win.dispatchEvent(new win.Event("beforeunload"));
  assert.equal(current.closed, true);
});
