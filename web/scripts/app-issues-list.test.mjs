// The issues page: filters, sorting, status tabs, paging and its error and empty states.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, click, type, pick, settleAll, text, win } from "./support/app-harness.mjs";

const page = (from, n) => Array.from({ length: n }, (_, i) => ({ id: `p-${from + i}`, title: `Paged ${from + i}`, status: "unresolved", event_count: 1, project_slug: "web" }));
const routes = defaultRoutes({
  "GET /api/v1/issues/sparklines": () => ({ sparklines: { "p-0": [1, 4, 2] } }),
  "GET /api/v1/issues": ({ url }) => (url.includes("offset=2") ? { issues: page(2, 2), hasMore: false, total: 4 } : { issues: page(0, 2), hasMore: true, total: 4, totalIsApproximate: true, counts: { open: 4, regressed: 0, resolved: 0, muted: 0, all: 4 } }),
});
const fetchStub = await boot(routes);
const issueCalls = () => fetchStub.calls.filter((c) => c.url.startsWith("/api/v1/issues?"));

test("the heading counts what is loaded, and sparklines fill in afterwards", () => {
  assert.equal(qa("[data-issue-id]").length, 2);
  assert.match(q("#issue-count").textContent, /2/);
  assert.ok(fetchStub.calls.some((c) => c.url.startsWith("/api/v1/issues/sparklines?ids=p-0%2Cp-1")));
  assert.equal(qa(".issue-row")[0].querySelectorAll(".spark-bar").length, 3);
});

test("Load more appends the next page with the current offset and ends the list", async () => {
  click(q("#load-more-issues"));
  await settleAll();
  assert.ok(issueCalls().some((c) => c.url.includes("offset=2")));
  assert.equal(qa("[data-issue-id]").length, 4);
  assert.ok(q("#issues-end"));
  assert.equal(q("#load-more-issues"), null);
});

test("a failed page load offers a retry that succeeds", async () => {
  routes["GET /api/v1/issues"] = ({ url }) => (url.includes("offset=2") ? { status: 500, body: "" } : { issues: page(0, 2), hasMore: true, total: 4 });
  await go("#/alerts");
  await go("#/issues");
  click(q("#load-more-issues"));
  await settleAll();
  assert.match(q("#load-more-issues").textContent, /Retry/);
  assert.match(text("#status-text") + text("body"), /Failed to load more issues/);
  routes["GET /api/v1/issues"] = ({ url }) => (url.includes("offset=2") ? { issues: page(2, 2), hasMore: false } : { issues: page(0, 2), hasMore: true });
  click(q("#load-more-issues"));
  await settleAll();
  assert.equal(qa("[data-issue-id]").length, 4);
});

test("the search box, the release filter and the sort select reload the list with matching parameters", async () => {
  await go("#/alerts");
  await go("#/issues");
  type(q("#issue-filter"), "  boom ");
  await settleAll();
  assert.match(issueCalls().at(-1).url, /query=boom|q=boom/);
  type(q("#issue-release"), "1.2.3");
  await settleAll();
  assert.match(issueCalls().at(-1).url, /release=1\.2\.3/);
  pick(q("#issue-sort"), "event_count");
  await settleAll();
  assert.match(issueCalls().at(-1).url, /sort=event_count/);
});

test("a status tab reloads with that status", async () => {
  const tab = qa("[data-status]").find((b) => b.dataset.status === "resolved");
  click(tab);
  await settleAll();
  assert.match(issueCalls().at(-1).url, /status=resolved/);
});

test("a failed load shows Unavailable and the error in the list", async () => {
  routes["GET /api/v1/issues"] = { status: 500, body: "index offline" };
  await go("#/alerts");
  await go("#/issues");
  assert.equal(q("#issue-count").textContent, "Unavailable");
  assert.match(text("#issue-list"), /Issues unavailable/);
});

test("with no issues the list shows the setup guide", async () => {
  routes["GET /api/v1/issues"] = { issues: [], hasMore: false, total: 0 };
  await go("#/alerts");
  await go("#/issues");
  assert.match(text("#issue-list"), /No issues yet/);
  assert.match(text("#issue-list"), /Smoke test/);
  await settleAll();
  assert.ok(win.location.hash.includes("issues"));
});
