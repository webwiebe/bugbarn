// The logs view: loading with explorer panels, filters kept in the hash, the live stream and row actions.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, type, pick, settleAll, text, win } from "./support/app-harness.mjs";

const ago = (m) => new Date(Date.now() - m * 60000).toISOString();
const routes = defaultRoutes({
  "GET /api/v1/runtime-config": { spanbarn: { enabled: false, ui_url: "https://spans.test" } },
  "GET /api/v1/logs/facets": ({ url }) => ({ key: url.includes("key=") ? "service" : "", items: url.includes("key=") ? [{ value: "api", count: 2 }] : [{ value: "service", count: 4 }], truncated: false }),
  "GET /api/v1/logs": { logs: [
    { id: 1, received_at: ago(2), level_num: 30, level: "info", message: "hello", data: { service: "api", trace_id: "t-1" }, project_slug: "web" },
    { id: 2, received_at: ago(1), level_num: 50, level: "error", message: "boom", project_slug: "web" },
  ] },
});
const fetchStub = await boot(routes, "#/logs");
const logCalls = () => fetchStub.calls.filter((c) => c.url.startsWith("/api/v1/logs?"));
const stream = () => win.EventSource.instances.filter((s) => s.url.includes("/logs/stream")).at(-1);

test("the view lists the logs with their pills and a trace link into SpanBarn", () => {
  assert.equal(qa(".log-row").length, 2);
  assert.match(text("#overview-view"), /hello/);
  assert.equal(q(".log-trace a").getAttribute("href"), "https://spans.test/traces/t-1");
  assert.ok(q(".log-hist-bar"), "the histogram is drawn");
  assert.ok(q("[data-facet-key='service']"), "the facet keys are listed");
});

test("clicking a row expands it, and clicking its trace link does not collapse it", () => {
  const row = q(".log-row");
  click(row);
  assert.ok(row.classList.contains("expanded"));
  click(q(".log-trace a"));
  assert.ok(row.classList.contains("expanded"));
  click(row);
  assert.equal(row.classList.contains("expanded"), false);
});

test("a pill adds an attribute filter to the hash and a chip removes it again", async () => {
  click(q(".log-pill"));
  await settleAll();
  assert.match(decodeURIComponent(win.location.hash), /service/);
  assert.ok(logCalls().at(-1).url.includes("service"));
  click(q("[data-attr-remove]"));
  await settleAll();
  assert.doesNotMatch(decodeURIComponent(win.location.hash), /service=api/);
});

test("a pill also reacts to Enter and Space from the keyboard", async () => {
  const pill = q(".log-pill");
  pill.dispatchEvent(new win.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  await settleAll();
  assert.match(decodeURIComponent(win.location.hash), /service/);
  click(q("[data-attr-remove]"));
  await settleAll();
  q(".log-pill").dispatchEvent(new win.KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true }));
  await settleAll();
  assert.match(decodeURIComponent(win.location.hash), /service/);
  click(q("[data-attr-remove]"));
  await settleAll();
});

test("the range select, the level select and the search box change the query", async () => {
  pick(q("#log-range"), "7d");
  await settleAll();
  assert.match(logCalls().at(-1).url, /from=/);
  pick(q("#log-level-filter"), "error");
  await settleAll();
  assert.match(win.location.hash, /level=error/);
  assert.match(logCalls().at(-1).url, /level/);
  type(q("#log-search"), "boo");
  type(q("#log-search"), "boom");
  await new Promise((r) => setTimeout(r, 400));
  await settleAll();
  assert.match(win.location.hash, /q=boom/);
  type(q("#log-search"), "");
  await new Promise((r) => setTimeout(r, 400));
  await settleAll();
  pick(q("#log-level-filter"), "");
  await settleAll();
});

test("a histogram bar narrows to its bucket and the range chip resets it", async () => {
  click(q("[data-bucket-start]"));
  await settleAll();
  assert.match(win.location.hash, /from=/);
  click(q("#log-range-reset"));
  await settleAll();
  assert.doesNotMatch(win.location.hash, /from=/);
});

test("facet keys open their values, a value adds a filter, and the key toggles closed", async () => {
  click(q("[data-facet-key='service']"));
  await settleAll();
  assert.ok(fetchStub.calls.some((c) => c.url.startsWith("/api/v1/logs/facets") && c.url.includes("key=service")));
  click(q("[data-facet-add-key='service']"));
  await settleAll();
  assert.match(decodeURIComponent(win.location.hash), /service/);
  click(q("[data-attr-remove]"));
  await settleAll();
  click(q("[data-facet-key='service']"));
  await settleAll();
  assert.equal(q("[data-facet-add-key]"), null);
});

test("the live stream prepends matching entries and drops the ones the filters exclude", async () => {
  const live = stream();
  live.onopen();
  assert.ok(q("#log-live-dot").classList.contains("connected"));
  live.emit({ id: 9, received_at: ago(0), level_num: 40, level: "warn", message: "fresh warning", project_slug: "web" });
  assert.match(q("#log-list .log-row").textContent, /fresh warning/);
  const count = qa("#log-list .log-row").length;
  win.location.hash = "#/logs?level=error";
  await settleAll();
  const filtered = stream();
  filtered.emit({ id: 10, received_at: ago(0), level_num: 30, level: "info", message: "too quiet", project_slug: "web" });
  assert.doesNotMatch(text("#log-list"), /too quiet/);
  filtered.emit({ id: 11, received_at: ago(0), level_num: 50, level: "error", message: "loud enough", project_slug: "web" });
  assert.match(text("#log-list"), /loud enough/);
  assert.ok(count >= 3, "the warning was added to the list");
  filtered.onmessage({ data: "garbage" });
  filtered.onerror();
  assert.equal(q("#log-live-dot").classList.contains("connected"), false);
});

test("Clear empties the list and an empty list shows its hint, a failed load shows no rows", async () => {
  click(q("#log-clear"));
  assert.match(text("#log-list"), /No log entries shown/);
  routes["GET /api/v1/logs"] = { status: 500, body: {} };
  await go("#/alerts");
  await go("#/logs");
  assert.equal(qa(".log-row").length, 0);
});
