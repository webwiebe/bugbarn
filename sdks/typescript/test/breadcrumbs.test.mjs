import assert from "node:assert/strict";
import test from "node:test";

import { addBreadcrumb, clearBreadcrumbs } from "../dist/esm/index.js";
import { getBreadcrumbs } from "../dist/esm/breadcrumbs.js";

function makeCrumb(message = "test") {
  return {
    timestamp: new Date().toISOString(),
    category: "manual",
    message,
  };
}

test("addBreadcrumb and getBreadcrumbs round-trip", () => {
  clearBreadcrumbs();
  addBreadcrumb(makeCrumb("hello"));
  const crumbs = getBreadcrumbs();
  assert.equal(crumbs.length, 1);
  assert.equal(crumbs[0].message, "hello");
  assert.equal(crumbs[0].category, "manual");
  clearBreadcrumbs();
});

test("getBreadcrumbs returns a copy, not the internal buffer", () => {
  clearBreadcrumbs();
  addBreadcrumb(makeCrumb("a"));
  const first = getBreadcrumbs();
  first.push(makeCrumb("injected"));
  const second = getBreadcrumbs();
  assert.equal(second.length, 1);
  clearBreadcrumbs();
});

test("ring buffer caps at 100 breadcrumbs (oldest dropped)", () => {
  clearBreadcrumbs();
  for (let i = 0; i < 110; i++) {
    addBreadcrumb(makeCrumb(`msg-${i}`));
  }
  const crumbs = getBreadcrumbs();
  assert.equal(crumbs.length, 100);
  // The first 10 messages (0–9) should be gone; the 11th (msg-10) is now first
  assert.equal(crumbs[0].message, "msg-10");
  assert.equal(crumbs[99].message, "msg-109");
  clearBreadcrumbs();
});

test("clearBreadcrumbs empties the buffer", () => {
  addBreadcrumb(makeCrumb("will be cleared"));
  clearBreadcrumbs();
  assert.equal(getBreadcrumbs().length, 0);
});
