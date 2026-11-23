// BrandTrace runtime: the SDK tag appears only when the server hands out a site key.
import test from "node:test";
import assert from "node:assert/strict";
import { boot, defaultRoutes, win } from "./support/app-harness.mjs";

const routes = defaultRoutes();
await boot(routes);
const brandtrace = await import("../src/brandtrace.ts");

const sdkTags = () => win.document.querySelectorAll('script[src="https://brandtrace.net/sdk.js"]');

test("fetchBrandTraceRuntime returns the brandtrace block, and undefined on errors", async () => {
  routes["GET /api/v1/runtime-config"] = { brandtrace: { enabled: true, siteKey: "k1" } };
  assert.deepEqual(await brandtrace.fetchBrandTraceRuntime(), { enabled: true, siteKey: "k1" });
  routes["GET /api/v1/runtime-config"] = { status: 500, body: {} };
  assert.equal(await brandtrace.fetchBrandTraceRuntime(), undefined);
  routes["GET /api/v1/runtime-config"] = () => {
    throw new Error("offline");
  };
  assert.equal(await brandtrace.fetchBrandTraceRuntime(), undefined);
});

test("startBrandTrace stays off without an enabled runtime and a key", () => {
  assert.equal(brandtrace.startBrandTrace(undefined), false);
  assert.equal(brandtrace.startBrandTrace({ enabled: false, siteKey: "k" }), false);
  assert.equal(brandtrace.startBrandTrace({ enabled: true }), false);
  assert.equal(sdkTags().length, 0);
});

test("startBrandTrace adds one SDK tag with the site key, once", () => {
  assert.equal(brandtrace.startBrandTrace({ enabled: true, siteKey: "abc123" }), true);
  assert.equal(sdkTags().length, 1);
  assert.equal(sdkTags()[0].getAttribute("data-site-key"), "abc123");
  assert.equal(brandtrace.startBrandTrace({ enabled: true, siteKey: "abc123" }), false);
  assert.equal(sdkTags().length, 1);
});

test("initBrandTrace reads the runtime config and does nothing when the key is absent", async () => {
  routes["GET /api/v1/runtime-config"] = {};
  await brandtrace.initBrandTrace();
  assert.equal(sdkTags().length, 1);
});
