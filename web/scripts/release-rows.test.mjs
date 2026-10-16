// Unit test for the rows of the issue page "Releases" block.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { releaseRows } from "../src/release-rows.ts";

const rel = (release, events) => ({
  release,
  events,
  first_seen: "2026-10-01T09:00:00Z",
  last_seen: "2026-10-01T10:00:00Z",
});

test("lists releases in the given order with their share of the sample", () => {
  const out = releaseRows([rel("0.3.0", 1), rel("0.2.0", 3)], 5);
  assert.deepEqual(out.rows.map((r) => [r.release, r.events, r.percent]), [
    ["0.3.0", 1, 20],
    ["0.2.0", 3, 60],
  ]);
  assert.equal(out.withoutRelease, 1);
  assert.equal(out.sampled, 5);
});

test("an empty list has no rows and counts every sampled event as without release", () => {
  const out = releaseRows([], 4);
  assert.equal(out.rows.length, 0);
  assert.equal(out.withoutRelease, 4);
});

test("a zero sample does not divide by zero", () => {
  const out = releaseRows([], 0);
  assert.equal(out.withoutRelease, 0);
  assert.deepEqual(out.rows, []);
});
