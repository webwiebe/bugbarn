// The Settings projects page lists the environments of the selected project.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, go, q, qa, text, win } from "./support/app-harness.mjs";

win.localStorage.setItem("bugbarn_project", "web");
const routes = defaultRoutes({
  "GET /api/v1/environments": { environments: [
    { environment: "production", issues: 12, open: 4, last_seen: new Date().toISOString() },
    { environment: "staging", issues: 1, open: 1 },
  ] },
});
const fetchStub = await boot(routes, "#/settings/projects");

test("the projects page lists each environment with its issue counts", async () => {
  await go("#/settings/projects");
  const rows = qa("[data-environment]");
  assert.equal(rows.length, 2);
  assert.match(text("[data-environment=production]"), /4 open of 12 issues/);
  assert.match(text("[data-environment=staging]"), /1 open of 1 issue(?!s)/);
  assert.ok(q("h3") && fetchStub.calls.some((c) => c.url.includes("/api/v1/environments")));
});
