// Unit test for the error timeline stacking and the "other" grouping.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { stackTimeline, projectColor, MAX_SERIES, OTHER_KEY } from "../src/timeline-stack.ts";

function project(slug, total) {
  return { slug, name: slug.toUpperCase(), total };
}

function data(projects, buckets) {
  return { range: "24h", bucket_seconds: 3600, projects, buckets };
}

test("stacks the segments of a bar in series order and sums the total", () => {
  const stacked = stackTimeline(
    data([project("web", 5), project("api", 2)], [
      { start: "2026-10-14T09:00:00Z", counts: {} },
      { start: "2026-10-14T10:00:00Z", counts: { api: 2, web: 3 } },
      { start: "2026-10-14T11:00:00Z", counts: { web: 2 } },
    ]),
  );
  assert.deepEqual(stacked.series.map((s) => s.key), ["web", "api"]);
  assert.deepEqual(stacked.bars[1].segments.map((s) => [s.key, s.count]), [["web", 3], ["api", 2]]);
  assert.equal(stacked.bars[1].total, 5);
  assert.equal(stacked.max, 5);
});

test("an empty bucket is a bar of zero with no segments", () => {
  const stacked = stackTimeline(data([project("web", 1)], [{ start: "t0", counts: {} }, { start: "t1", counts: { web: 1 } }]));
  assert.equal(stacked.bars[0].total, 0);
  assert.deepEqual(stacked.bars[0].segments, []);
});

test("eight projects or fewer get no other series", () => {
  const projects = Array.from({ length: MAX_SERIES }, (_, i) => project(`p${i}`, 100 - i));
  const stacked = stackTimeline(data(projects, [{ start: "t0", counts: { p0: 1 } }]));
  assert.equal(stacked.series.length, MAX_SERIES);
  assert.equal(stacked.series.some((s) => s.isOther), false);
});

test("projects beyond the eighth are summed into one other series", () => {
  const projects = Array.from({ length: 11 }, (_, i) => project(`p${i}`, 100 - i));
  const counts = { p0: 4, p8: 2, p9: 3, p10: 1 };
  const stacked = stackTimeline(data(projects, [{ start: "t0", counts }]));
  assert.equal(stacked.series.length, MAX_SERIES + 1);
  const other = stacked.series[MAX_SERIES];
  assert.equal(other.key, OTHER_KEY);
  assert.equal(other.isOther, true);
  assert.equal(other.name, "Other (3)");
  assert.equal(other.total, 100 - 8 + (100 - 9) + (100 - 10));
  assert.deepEqual(stacked.bars[0].segments.map((s) => [s.key, s.count]), [["p0", 4], [OTHER_KEY, 6]]);
  assert.equal(stacked.bars[0].total, 10);
});

test("a custom series limit moves the cut", () => {
  const stacked = stackTimeline(data([project("a", 3), project("b", 2), project("c", 1)], [{ start: "t0", counts: { a: 1, b: 1, c: 1 } }]), 2);
  assert.deepEqual(stacked.series.map((s) => s.key), ["a", "b", OTHER_KEY]);
  assert.equal(stacked.bars[0].segments.at(-1).count, 1);
});

test("a project keeps its colour whatever else is on the chart", () => {
  assert.equal(projectColor("bugbarn-service"), projectColor("bugbarn-service"));
  assert.notEqual(projectColor("bugbarn-service"), projectColor("professionals"));
  const alone = stackTimeline(data([project("web", 1)], [])).series[0].color;
  const crowded = stackTimeline(data([project("api", 9), project("web", 1)], [])).series[1].color;
  assert.equal(alone, crowded);
});

test("a name falls back to the slug", () => {
  const stacked = stackTimeline(data([{ slug: "web", name: "", total: 1 }], []));
  assert.equal(stacked.series[0].name, "web");
});
