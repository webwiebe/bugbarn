// The ratchet's own test. A gate that has quietly stopped failing looks exactly
// like a clean repo, which is why it is tested before it is trusted.
//
// `compare` and `tally` are the whole decision, so they are the whole test
// surface: running oxlint itself is not what can silently break.

import test from "node:test";
import assert from "node:assert/strict";
import { compare, tally, refreshCommand, BASELINE_FILE, BUDGET_RULES } from "./oxlint-ratchet.mjs";

const clean = { total: 0, counts: {} };

test("a clean tree against a clean baseline passes", () => {
  const res = compare(clean, {});
  assert.equal(res.ok, true);
  assert.equal(res.total, 0);
  assert.deepEqual(res.regressions, []);
});

test("a NEW rule appearing fails", () => {
  const res = compare(clean, { "eslint(no-empty)": 1 });
  assert.equal(res.ok, false, "a new finding must fail the ratchet");
  assert.deepEqual(res.regressions, [{ rule: "eslint(no-empty)", was: 0, now: 1 }]);
});

test("an existing rule going UP fails, and names the rule", () => {
  const base = { total: 2, counts: { "eslint(prefer-const)": 2 } };
  const res = compare(base, { "eslint(prefer-const)": 3 });
  assert.equal(res.ok, false);
  assert.equal(res.regressions.length, 1);
  assert.equal(res.regressions[0].rule, "eslint(prefer-const)");
  assert.equal(res.regressions[0].now, 3);
});

test("an unchanged count passes", () => {
  const base = { total: 2, counts: { "eslint(prefer-const)": 2 } };
  assert.equal(compare(base, { "eslint(prefer-const)": 2 }).ok, true);
});

test("going DOWN fails, so the baseline has to follow the work down", () => {
  const base = { total: 5, counts: { "eslint(prefer-const)": 5 } };
  const res = compare(base, { "eslint(prefer-const)": 1 });
  assert.equal(res.ok, false, "a stale allowance must fail, not pass quietly");
  assert.deepEqual(res.improvements, [{ rule: "eslint(prefer-const)", was: 5, now: 1 }]);
  assert.deepEqual(res.regressions, [], "an improvement is not a regression");
});

test("a rule cleared entirely is an improvement, and still fails", () => {
  const base = { total: 3, counts: { "eslint(prefer-const)": 3 } };
  const res = compare(base, {});
  assert.equal(res.ok, false);
  assert.equal(res.improvements[0].now, 0);
  assert.deepEqual(res.regressions, []);
});

test("one rule improving does not mask another regressing", () => {
  const base = { total: 4, counts: { "eslint(prefer-const)": 4 } };
  const res = compare(base, { "eslint(prefer-const)": 2, "eslint(no-empty)": 2 });
  assert.equal(res.total, base.total, "totals match, so only a per-rule check can catch this");
  assert.equal(res.ok, false, "a per-rule regression must fail even when the total is flat");
  assert.deepEqual(res.regressions, [{ rule: "eslint(no-empty)", was: 0, now: 2 }]);
});

test("a missing counts key in the baseline is treated as zero, not a crash", () => {
  assert.equal(compare({}, { "eslint(no-empty)": 1 }).ok, false);
});

test("the refresh command is printed verbatim", () => {
  assert.equal(refreshCommand(), "node scripts/oxlint-ratchet.mjs --write");
  assert.equal(BASELINE_FILE, "oxlint-baseline.json");
});

// The budget rules belong to scripts/check-ts-budget.sh. If tally ever counted
// them here, this zero baseline would fail on the budget soak's 18 findings;
// if it dropped them entirely, the budget soak would read zero and go stale.
test("budget-rule findings go to the budget list and nowhere else", () => {
  const { counts, budget } = tally([
    { code: "eslint(complexity)" },
    { code: "eslint(max-lines-per-function)" },
    { code: "eslint(max-lines)" },
    { code: "eslint(prefer-const)" },
  ]);
  assert.deepEqual(counts, { "eslint(prefer-const)": 1 });
  assert.equal(budget.length, 3);
  assert.deepEqual(budget.map((f) => f.code).sort(), [...BUDGET_RULES].sort());
});
