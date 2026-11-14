// SpanBarn runtime, the telemetry read helper and the issues-page error timeline.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, settleAll, win } from "./support/app-harness.mjs";

const routes = defaultRoutes();
const fetchStub = await boot(routes);
const spanbarn = await import("../src/spanbarn.ts");
const telemetry = await import("../src/telemetry-api.ts");
const core = await import("../src/core.ts");

test("fetchSpanBarnRuntime returns the spanbarn block, and undefined on errors", async () => {
  routes["GET /api/v1/runtime-config"] = { spanbarn: { enabled: true, endpoint: "/relay", service: "web" } };
  assert.deepEqual(await spanbarn.fetchSpanBarnRuntime(), { enabled: true, endpoint: "/relay", service: "web" });
  routes["GET /api/v1/runtime-config"] = { status: 500, body: {} };
  assert.equal(await spanbarn.fetchSpanBarnRuntime(), undefined);
  routes["GET /api/v1/runtime-config"] = () => {
    throw new Error("offline");
  };
  assert.equal(await spanbarn.fetchSpanBarnRuntime(), undefined);
});

test("startSpanBarn needs an enabled runtime with an endpoint and service", () => {
  assert.equal(spanbarn.startSpanBarn(undefined, "/e"), false);
  assert.equal(spanbarn.startSpanBarn({ enabled: false, endpoint: "/r", service: "s" }, "/e"), false);
  assert.equal(spanbarn.startSpanBarn({ enabled: true, service: "s" }, "/e"), false);
  assert.equal(spanbarn.startSpanBarn({ enabled: true, endpoint: "/r" }, "/e"), false);
});

test("startSpanBarn starts the SDK for a complete runtime and reports false when it throws", () => {
  const started = spanbarn.startSpanBarn({ enabled: true, endpoint: "https://relay.test/v1", service: "bugbarn-web" }, "/api/v1/client-errors");
  assert.equal(typeof started, "boolean");
  assert.equal(spanbarn.startSpanBarn({ enabled: true, endpoint: "", service: "x" }, "/e"), false);
});

test("telemetryGet returns the parsed body on success", async () => {
  routes["GET /api/v1/telemetry/ok"] = { rows: [1] };
  assert.deepEqual(await telemetry.telemetryGet("/api/v1/telemetry/ok"), { rows: [1] });
});

test("telemetryGet keeps the server's message on an error and falls back to the status", async () => {
  routes["GET /api/v1/telemetry/missing"] = { status: 503, body: "security.db has not been created yet" };
  await assert.rejects(telemetry.telemetryGet("/api/v1/telemetry/missing"), /security\.db has not been created yet/);
  routes["GET /api/v1/telemetry/empty"] = { status: 500, body: "" };
  await assert.rejects(telemetry.telemetryGet("/api/v1/telemetry/empty"), /^Error: 500/);
});

test("telemetryGet on a 401 shows the login screen", async () => {
  routes["GET /api/v1/telemetry/private"] = { status: 401, body: "nope" };
  await assert.rejects(telemetry.telemetryGet("/api/v1/telemetry/private"), /nope/);
  assert.equal(core.state.authenticated, false);
  assert.equal(win.document.getElementById("login-screen").hidden, false);
  core.state.authenticated = true;
  win.document.getElementById("login-screen").hidden = true;
});

test("the error timeline loads for the stored range and draws its buckets and legend", async () => {
  await go("#/issues");
  const slot = q("#error-timeline-slot");
  assert.ok(slot.querySelector("[data-timeline-range]"));
  assert.ok(fetchStub.calls.some((c) => /\/api\/v1\/events\/timeline\?range=24h/.test(c.url)));
  assert.ok(slot.querySelector("[data-timeline-project]"), "the legend lists the projects");
});

test("picking another range stores it, redraws and reloads the data", async () => {
  const buttons = qa("#error-timeline-slot [data-timeline-range]");
  const other = buttons.find((b) => b.dataset.timelineRange !== "24h");
  const range = other.dataset.timelineRange;
  const before = fetchStub.calls.length;
  click(other);
  await settleAll();
  assert.equal(win.localStorage.getItem("bugbarn.errorTimelineRange"), range);
  assert.ok(fetchStub.calls.slice(before).some((c) => c.url.includes(`/events/timeline?range=${range}`)));
  click(qa("#error-timeline-slot [data-timeline-range]").find((b) => b.dataset.timelineRange === range));
  assert.equal(fetchStub.calls.slice(before).filter((c) => c.url.includes("/events/timeline")).length, 1, "the same range is a no-op");
});

test("a failed timeline load shows the failure state, and the legend selects a project", async () => {
  routes["GET /api/v1/events/timeline"] = { status: 500, body: {} };
  click(qa("#error-timeline-slot [data-timeline-range]").find((b) => b.dataset.timelineRange === "24h"));
  await settleAll();
  assert.ok(q("#error-timeline-slot").textContent.length > 0);
  routes["GET /api/v1/events/timeline"] = { range: "24h", bucket_seconds: 3600, buckets: [{ start: new Date().toISOString(), counts: { web: 2 } }], projects: [{ slug: "web", name: "Web", total: 2 }] };
  click(qa("#error-timeline-slot [data-timeline-range]").find((b) => b.dataset.timelineRange !== "24h"));
  await settleAll();
  let selected = null;
  win.document.addEventListener("bugbarn:select-project", (e) => {
    selected = e.detail;
  });
  click(q("#error-timeline-slot [data-timeline-project]"));
  assert.equal(selected, "web");
  await settleAll();
});
