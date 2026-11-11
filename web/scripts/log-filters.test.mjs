// Unit test for the Logs view filter state and its URL form.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import {
  defaultLogFilters, liveCompatible, logFiltersHash, logQueryParams, logWindow, parseLogFilters, splitAttr,
  withAttr, withoutAttr,
} from "../src/log-filters.ts";

test("the plain hash gives the defaults and the defaults give the plain hash", () => {
  assert.deepEqual(parseLogFilters("#/logs"), defaultLogFilters());
  assert.equal(logFiltersHash(defaultLogFilters()), "#/logs");
});

test("filters survive a round trip through the hash", () => {
  const f = {
    range: "7d", from: "", to: "", level: "error", q: "timeout & retry",
    attrs: ["user=a b", "route=/x?y=1"], facetKey: "user",
  };
  assert.deepEqual(parseLogFilters(logFiltersHash(f)), f);
});

test("a clicked bucket survives the round trip; a reversed one is dropped", () => {
  const f = { ...defaultLogFilters(), from: "2026-11-11T09:00:00.000Z", to: "2026-11-11T09:05:00.000Z" };
  assert.deepEqual(parseLogFilters(logFiltersHash(f)), f);
  const bad = "#/logs?from=2026-11-11T09:05:00Z&to=2026-11-11T09:00:00Z";
  assert.equal(parseLogFilters(bad).from, "");
});

test("unknown range and attributes without a key fall back", () => {
  const f = parseLogFilters("#/logs?range=90d&attr=novalue&attr==x&attr=ok=1");
  assert.equal(f.range, "24h");
  assert.deepEqual(f.attrs, ["ok=1"]);
});

test("the window is the range ending now, or the clicked bucket", () => {
  const now = Date.parse("2026-11-11T10:00:00Z");
  const w = logWindow({ ...defaultLogFilters(), range: "1h" }, now);
  assert.equal(w.from, "2026-11-11T09:00:00.000Z");
  assert.equal(w.to, "2026-11-11T10:00:01.000Z");
  const f = { ...defaultLogFilters(), from: "2026-11-11T09:00:00.000Z", to: "2026-11-11T09:05:00.000Z" };
  assert.deepEqual(logWindow(f, now), { from: f.from, to: f.to });
});

test("query params carry the window and every filter", () => {
  const f = { ...defaultLogFilters(), level: "warn", q: "x", attrs: ["a=1", "b=2"] };
  const p = logQueryParams(f, Date.parse("2026-11-11T10:00:00Z"));
  assert.equal(p.get("level"), "warn");
  assert.equal(p.get("q"), "x");
  assert.deepEqual(p.getAll("attr"), ["a=1", "b=2"]);
  assert.ok(p.get("from") && p.get("to"));
});

test("adding an attribute twice keeps one filter and removing drops it", () => {
  let f = withAttr(defaultLogFilters(), "user", "u1");
  f = withAttr(f, "user", "u1");
  assert.deepEqual(f.attrs, ["user=u1"]);
  assert.deepEqual(withoutAttr(f, "user=u1").attrs, []);
});

test("the live stream only matches level and text filters", () => {
  assert.equal(liveCompatible(defaultLogFilters()), true);
  assert.equal(liveCompatible(withAttr(defaultLogFilters(), "a", "1")), false);
  assert.equal(liveCompatible({ ...defaultLogFilters(), from: "x", to: "y" }), false);
});

test("an attribute splits at the first equals sign", () => {
  assert.deepEqual(splitAttr("q=a=b"), { key: "q", value: "a=b" });
});
