// Visits every route of the real app and presses what each view renders.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, go, text, q, qa, pressAll, settleAll, win } from "./support/app-harness.mjs";

const fetchStub = await boot();
const called = (fragment, method = "GET") => fetchStub.calls.some((c) => c.method === method && c.url.includes(fragment));

const routes = [
  ["#/releases", "Releases", "/api/v1/releases"],
  ["#/alerts", "Alerts", "/api/v1/alerts"],
  ["#/logs", "Logs", "/api/v1/logs?"],
  ["#/security", "Security", "/api/v1/telemetry/security"],
  ["#/hosts", "Hosts", "/api/v1/telemetry/hosts"],
  ["#/rules", "Detection rules", "/api/v1/detection/rules"],
  ["#/monitors", "Monitors", "/api/v1/monitors"],
  ["#/settings", "Settings", "/api/v1/settings"],
  ["#/settings/projects", "Projects", "/api/v1/projects?usage=true"],
  ["#/settings/volume", "Volume", "/api/v1/projects"],
  ["#/settings/preferences", "Preferences", "/api/v1/settings"],
  ["#/settings/keys", "API Keys", "/api/v1/apikeys"],
  ["#/settings/system", "System", "/api/v1/health"],
  ["#/account", "Account", "/api/v1/me"],
];

for (const [hash, title, endpoint] of routes) {
  test(`${hash} renders, loads ${endpoint} and survives every button`, async () => {
    await go(hash);
    assert.ok(text("#topbar-title").includes(title) || text("title").includes(title) || win.document.title.includes(title), `title for ${hash}: ${win.document.title}`);
    assert.ok(called(endpoint), `${endpoint} was fetched`);
    const pressed = await pressAll("#overview-view");
    await pressAll("#detail-view");
    assert.ok(Array.isArray(pressed));
    await settleAll();
  });
}
