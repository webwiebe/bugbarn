#!/usr/bin/env node
// oxlint, as a ratchet. Ported from brandtrace (apps/web/scripts).
//
// Why oxlint at all: typescript-eslint consumes the TypeScript compiler API
// directly and cannot load against TS 7. oxlint parses TypeScript itself in
// Rust, so it is unaffected, which makes it the migration target.
//
// The gate is symmetric: a rule above its baseline fails, and so does a rule
// BELOW it. An improvement fails because the baseline has to follow the work
// down: clear five findings of a rule baselined at twelve and a stale
// allowance of twelve is room for those five to come back with the gate still
// green. The fix is one paste, and the script prints it.
//
// The three size/complexity budget rules are counted by
// scripts/check-ts-budget.sh against its own baseline (see BUDGET_RULES), so
// this baseline can sit at zero while that soak still has findings.
//
// `--write` refreshes the baseline after a deliberate change.
// `--report-only` prints the same report but always exits 0.
// `--budget` prints the budget-rule findings as JSON and exits; the
// ts-budget soak reads it.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const BASELINE_FILE = "oxlint-baseline.json";
export const LINT_TARGETS = ["src"];
export const BUDGET_RULES = [
  "eslint(complexity)",
  "eslint(max-lines)",
  "eslint(max-lines-per-function)",
];

/**
 * The command that refreshes the baseline. Printed verbatim on every failure
 * so the fix is a paste, not a guess.
 */
export function refreshCommand() {
  return "node scripts/oxlint-ratchet.mjs --write";
}

/**
 * Compare current counts against the baseline.
 *
 *   - a rule above its baseline count (or absent from the baseline) -> regression
 *   - a rule below it (or gone entirely)                            -> improvement
 *
 * Both fail, so `ok` requires neither.
 *
 * Pure, so the gate can be tested without running oxlint.
 * Returns { ok, regressions, improvements, total, baselineTotal }.
 */
export function compare(baseline, counts) {
  const regressions = [];
  const improvements = [];
  const rules = new Set([...Object.keys(baseline.counts ?? {}), ...Object.keys(counts)]);
  for (const rule of rules) {
    const was = baseline.counts?.[rule] ?? 0;
    const now = counts[rule] ?? 0;
    if (now > was) regressions.push({ rule, was, now });
    else if (now < was) improvements.push({ rule, was, now });
  }
  const total = Object.values(counts).reduce((a, b) => a + b, 0);
  const baselineTotal = Object.values(baseline.counts ?? {}).reduce((a, b) => a + b, 0);
  return {
    ok: regressions.length === 0 && improvements.length === 0,
    regressions,
    improvements,
    total,
    baselineTotal,
  };
}

/**
 * Split findings into per-rule counts for the ratchet and the budget-rule
 * findings for the ts-budget soak. Pure, so the split is testable.
 */
export function tally(findings) {
  const counts = {};
  const budget = [];
  for (const f of findings) {
    const code = f.code ?? "unknown";
    if (BUDGET_RULES.includes(code)) {
      budget.push(f);
      continue;
    }
    counts[code] = (counts[code] ?? 0) + 1;
  }
  return { counts, budget };
}

export function runOxlint(webDir) {
  const bin = path.join(webDir, "node_modules", ".bin", "oxlint");
  if (!fs.existsSync(bin)) {
    throw new Error(`oxlint is not installed at ${bin}; run pnpm install in web/ first`);
  }
  let raw = "";
  try {
    raw = execFileSync(bin, ["-f", "json", ...LINT_TARGETS], {
      cwd: webDir,
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
    });
  } catch (err) {
    if (!err.stdout) throw new Error(`oxlint did not run: ${err.message}`, { cause: err });
    raw = err.stdout;
  }
  // oxlint returns a bare array in some versions and { diagnostics } in others.
  const parsed = JSON.parse(raw);
  return Array.isArray(parsed) ? parsed : (parsed.diagnostics ?? []);
}

function report(res, counts, reportOnly) {
  console.log(`oxlint: ${res.total} findings (baseline ${res.baselineTotal})`);
  for (const [rule, n] of Object.entries(counts).sort((a, b) => b[1] - a[1])) {
    console.log(`  ${String(n).padStart(4)}  ${rule}`);
  }
  if (res.ok) {
    console.log("");
    console.log("oxlint: on the baseline, exactly.");
    return;
  }

  // Report-only says the same things, on stdout, and exits 0.
  const say = reportOnly ? console.log : console.error;
  say("");
  say(
    reportOnly
      ? "oxlint ratchet: the tree and the baseline disagree, but this run is report-only."
      : "oxlint ratchet FAILED: the tree and the baseline disagree.",
  );
  if (res.regressions.length) {
    say("");
    say("New findings that are not in the baseline:");
    for (const r of res.regressions) say(`  ${r.rule}: ${r.was} -> ${r.now}`);
    say("");
    say("Fix them, or if the finding is wrong, suppress it at the call site with");
    say("`// oxlint-disable-next-line <rule>` and a comment saying why. The");
    say("directive applies to the line DIRECTLY below it.");
  }
  if (res.improvements.length) {
    say("");
    say("Findings below the baseline, so the baseline is now stale:");
    for (const i of res.improvements) say(`  ${i.rule}: ${i.was} -> ${i.now}`);
    say("");
    say("Lock the improvement in:");
    say(`  ${refreshCommand()}`);
  }
}

function main() {
  const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const baselinePath = path.join(webDir, BASELINE_FILE);
  const { counts, budget } = tally(runOxlint(webDir));

  if (process.argv.includes("--budget")) {
    console.log(JSON.stringify(budget));
    return;
  }

  const total = Object.values(counts).reduce((a, b) => a + b, 0);
  if (process.argv.includes("--write")) {
    fs.writeFileSync(baselinePath, `${JSON.stringify({ total, counts }, null, 2)}\n`);
    console.log(`oxlint baseline written: ${total} findings`);
    return;
  }

  const baseline = fs.existsSync(baselinePath)
    ? JSON.parse(fs.readFileSync(baselinePath, "utf8"))
    : { total: 0, counts: {} };
  const res = compare(baseline, counts);
  if (res.ok && res.total === 0) {
    console.log("oxlint: clean");
    return;
  }
  const reportOnly = process.argv.includes("--report-only");
  report(res, counts, reportOnly);
  if (res.ok || reportOnly) return;
  process.exit(1);
}

// Only run when invoked directly, so the test can import the pure helpers.
if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main();
}
