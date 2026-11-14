// The coverage ratchet's own test. A gate that has quietly stopped failing looks
// exactly like healthy coverage, which is why it is tested before it is trusted.
// `evaluate`, `readSummary` and `filesBelow` are the whole decision, so they
// are the whole test surface: running c8 is not what can silently break.

import test from "node:test";
import assert from "node:assert/strict";
import { BASELINE_FILE, MAX_DRIFT, MINIMUM, evaluate, filesBelow, readSummary, suggestedBaseline } from "./coverage-ratchet.mjs";

test("a measurement inside the band passes", () => {
  const res = evaluate({ total: 85.5, baseline: 84.0 });
  assert.equal(res.ok, true);
  assert.deepEqual(res.failures, []);
});

test("a measurement exactly on the baseline passes", () => {
  assert.equal(evaluate({ total: 84.0, baseline: 84.0 }).ok, true);
});

test("a drop below the baseline fails", () => {
  const res = evaluate({ total: 83.9, baseline: 84.0 });
  assert.equal(res.ok, false);
  assert.match(res.failures[0], /dropped below the baseline of 84/);
});

test("a gain beyond the drift band fails and prints the new floor", () => {
  const res = evaluate({ total: 84.0 + MAX_DRIFT + 0.1, baseline: 84.0 });
  assert.equal(res.ok, false);
  assert.match(res.failures[0], /unprotected/);
  assert.ok(res.failures[0].includes(`echo ${suggestedBaseline(87.1)} > ${BASELINE_FILE}`));
});

test("a gain of exactly the drift band still passes", () => {
  assert.equal(evaluate({ total: 84.0 + MAX_DRIFT, baseline: 84.0 }).ok, true);
});

test("below the fixed minimum fails even when it matches the baseline", () => {
  const res = evaluate({ total: 79.9, baseline: 79.9 });
  assert.equal(res.ok, false);
  assert.match(res.failures[0], new RegExp(`below the fixed minimum of ${MINIMUM}%`));
});

test("a missing total or a missing baseline fails", () => {
  assert.equal(evaluate({ total: Number.NaN, baseline: 84 }).ok, false);
  const res = evaluate({ total: 90, baseline: Number.NaN });
  assert.equal(res.ok, false);
  assert.match(res.failures[0], /baseline is not a number/);
});

test("the suggested baseline sits one point under the measurement", () => {
  assert.equal(suggestedBaseline(86.47), "85.4");
  assert.equal(suggestedBaseline(90), "89.0");
});

test("readSummary pulls the line total and per-file percentages out of c8's report", () => {
  const summary = {
    total: { lines: { pct: 91.5 } },
    "/w/src/a.ts": { lines: { pct: 100 } },
    "/w/src/b.ts": { lines: { pct: 40 } },
  };
  const { total, files } = readSummary(summary);
  assert.equal(total, 91.5);
  assert.deepEqual(files.map((f) => f.name), ["/w/src/a.ts", "/w/src/b.ts"]);
  assert.ok(Number.isNaN(readSummary({}).total));
});

test("filesBelow lists files under the minimum, worst first", () => {
  const files = [{ name: "a", pct: 79.9 }, { name: "b", pct: 100 }, { name: "c", pct: 10 }, { name: "d", pct: 80 }];
  assert.deepEqual(filesBelow(files).map((f) => f.name), ["c", "a"]);
});
