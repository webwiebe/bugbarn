// Drives the issues list and issue/event detail through the real app.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, go, text, q, qa, click, settleAll, win } from "./support/app-harness.mjs";

const fetchStub = await boot();

test("the issues list renders the loaded issues and their counts", () => {
  assert.match(text("#overview-view"), /TypeError number 1/);
  assert.ok(qa("[data-issue-id]").length >= 3);
  assert.ok(fetchStub.calls.some((c) => c.url.startsWith("/api/v1/issues?limit=50")));
});

test("clicking an issue routes to its detail and loads events", async () => {
  click(q("[data-issue-id]"));
  await settleAll();
  assert.match(win.location.hash, /#\/issues\/issue-1/);
  assert.match(text("#detail-title"), /TypeError number 1/);
  assert.ok(fetchStub.calls.some((c) => c.url.includes("/issues/issue-1/events?limit=25")));
});
