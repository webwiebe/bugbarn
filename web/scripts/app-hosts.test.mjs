// The hosts view: the list, stale marking, the per-host chart cards and their ranges.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, settleAll, text, win } from "./support/app-harness.mjs";

const routes = defaultRoutes();
const fetchStub = await boot(routes, "#/hosts");
const seriesCalls = () => fetchStub.calls.filter((c) => c.url.includes("/hosts/h1/series"));

test("the host list shows each host with its cores and a live or stale badge", () => {
  const rows = qa(".host-row");
  assert.deepEqual(rows.map((r) => r.dataset.host), ["h1", "h2"]);
  assert.match(rows[0].textContent, /4.*live/s);
  assert.match(rows[1].textContent, /\?.*stale/s);
  assert.match(text("#overview-view"), /A host is stale after 5 minutes/);
});

test("a list error is shown in place of the table", async () => {
  routes["GET /api/v1/telemetry/hosts/h1"] = { status: 503, body: "" };
  routes["GET /api/v1/telemetry/hosts"] = { status: 503, body: "metrics.db has not been created yet" };
  await go("#/alerts");
  await go("#/hosts");
  assert.match(text("#overview-view"), /metrics\.db has not been created yet/);
});

test("with no hosts the view says so", async () => {
  routes["GET /api/v1/telemetry/hosts"] = { hosts: [] };
  await go("#/alerts");
  await go("#/hosts");
  assert.match(text("#overview-view"), /No host has reported metrics yet/);
  routes["GET /api/v1/telemetry/hosts"] = { hosts: [{ host: "h1", cores: 4, lastSeen: new Date().toISOString() }, { host: "h2", cores: 0, lastSeen: new Date(Date.now() - 36e6).toISOString() }] };
});

test("clicking a row opens the host and loads a chart card per metric kind", async () => {
  await go("#/alerts");
  await go("#/hosts");
  click(q(".host-row td:last-child"));
  await settleAll();
  assert.equal(win.location.hash, "#/hosts/h1");
  const titles = qa(".host-chart h3").map((h) => h.textContent);
  assert.deepEqual(titles, ["CPU utilization", "Load (1m), 4 cores", "Memory available", "fs./.used_pct", "net.eth0.rx_bytes_per_s"]);
  const body = text("#overview-view");
  assert.match(body, /now 2048\.00, peak 2048\.00|now 2\.00 KB/);
  assert.match(body, /now \d+\.\d%/);
  assert.match(body, /KB\/s/);
  assert.match(body, /Last seen/);
});

test("clicking the link inside a row does not double-navigate", async () => {
  await go("#/hosts");
  const link = q(".host-row a");
  assert.equal(link.getAttribute("href"), "#/hosts/h1");
});

test("a range button reloads the charts for that window", async () => {
  await go("#/hosts/h1");
  const buttons = qa("[data-host-range]");
  assert.ok(buttons.length > 1);
  const target = buttons.find((b) => !b.classList.contains("active"));
  const before = seriesCalls().length;
  click(target);
  await settleAll();
  assert.ok(seriesCalls().length > before);
  assert.ok(qa("[data-host-range].active").some((b) => b.dataset.hostRange === target.dataset.hostRange));
});

test("a chart request failure shows its message", async () => {
  routes["GET /api/v1/telemetry/hosts/h1/series"] = { status: 500, body: "series backend down" };
  await go("#/hosts");
  await go("#/hosts/h1");
  assert.match(text("#overview-view"), /series backend down/);
});

test("a host without chartable metrics says so", async () => {
  routes["GET /api/v1/telemetry/hosts/h1/metrics"] = { metrics: ["unrelated"] };
  await go("#/hosts");
  await go("#/hosts/h1");
  assert.match(text("#overview-view"), /no chartable metrics/);
});

test("empty series render the no-samples state and an unknown host has no last-seen line", async () => {
  routes["GET /api/v1/telemetry/hosts/h1/metrics"] = { metrics: ["cpu.util", "mem.avail_pct"] };
  routes["GET /api/v1/telemetry/hosts/h1/series"] = { metric: "cpu.util", resolution: "1m", points: [] };
  await go("#/hosts");
  await go("#/hosts/h1");
  assert.match(text("#overview-view"), /no data/);
  assert.match(text("#overview-view"), /No samples in this range/);
  await go("#/hosts/ghost");
  assert.doesNotMatch(text("#overview-view"), /Last seen/);
});
