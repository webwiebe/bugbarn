import assert from "node:assert/strict";
import test from "node:test";

import { clearBreadcrumbs } from "../dist/esm/index.js";
import { getBreadcrumbs } from "../dist/esm/breadcrumbs.js";
import { installAutoInterceptors, resetInterceptors } from "../dist/esm/interceptors.js";

test("installAutoInterceptors is idempotent — calling twice does not double-wrap console", () => {
  resetInterceptors();
  clearBreadcrumbs();

  installAutoInterceptors();
  installAutoInterceptors(); // second call must be a no-op

  // Trigger one console.info message
  console.info("idempotent-test");

  const crumbs = getBreadcrumbs().filter((c) => c.message === "idempotent-test");
  // Should be exactly 1 breadcrumb despite calling install twice
  assert.equal(crumbs.length, 1);

  clearBreadcrumbs();
  resetInterceptors();
});

test("console interceptor adds a breadcrumb for each wrapped method", () => {
  resetInterceptors();
  clearBreadcrumbs();
  installAutoInterceptors();

  console.log("log-msg");
  console.warn("warn-msg");
  console.error("error-msg");
  console.info("info-msg");
  console.debug("debug-msg");

  const crumbs = getBreadcrumbs();
  const categories = crumbs.map((c) => c.category);
  assert.ok(categories.every((c) => c === "console"), "all crumbs should have category 'console'");

  const messages = crumbs.map((c) => c.message);
  assert.ok(messages.includes("log-msg"));
  assert.ok(messages.includes("warn-msg"));
  assert.ok(messages.includes("error-msg"));
  assert.ok(messages.includes("info-msg"));
  assert.ok(messages.includes("debug-msg"));

  // Level mapping: "log" → "log", others match their method name
  const logCrumb = crumbs.find((c) => c.message === "log-msg");
  assert.equal(logCrumb?.level, "log");
  const warnCrumb = crumbs.find((c) => c.message === "warn-msg");
  assert.equal(warnCrumb?.level, "warn");

  clearBreadcrumbs();
  resetInterceptors();
});

test("installAutoInterceptors does not throw in an environment without fetch", () => {
  resetInterceptors();
  clearBreadcrumbs();

  const originalFetch = globalThis.fetch;
  // Simulate environment without fetch
  delete globalThis.fetch;

  try {
    assert.doesNotThrow(() => installAutoInterceptors());
  } finally {
    if (originalFetch !== undefined) {
      globalThis.fetch = originalFetch;
    }
    clearBreadcrumbs();
    resetInterceptors();
  }
});

test("installAutoInterceptors does not throw in an environment without XMLHttpRequest", () => {
  resetInterceptors();
  clearBreadcrumbs();

  // XMLHttpRequest is not available in Node.js — this test confirms no throw
  assert.doesNotThrow(() => installAutoInterceptors());

  clearBreadcrumbs();
  resetInterceptors();
});

test("installAutoInterceptors does not throw in an environment without window", () => {
  resetInterceptors();
  clearBreadcrumbs();

  // window is not available in Node.js — this test confirms no throw
  assert.doesNotThrow(() => installAutoInterceptors());

  clearBreadcrumbs();
  resetInterceptors();
});
