#!/usr/bin/env node
// Layer boundaries for the web app, as a ratchet. The rules are in
// .dependency-cruiser.cjs; boundaries-baseline.json lists the violations that
// already existed and can only shrink:
//
//   - a violation that is not in the baseline -> NEW (move the import)
//   - a baselined violation that is gone      -> STALE (refresh the baseline)
//
// `--write` refreshes the baseline after a deliberate change.
// Report only until `--enforce` is passed: problems print as warnings and the
// exit code stays 0.
//
// dependency-cruiser cruises zero modules without failing when it cannot parse
// the sources (it did exactly that under typescript 7 before swc was set as the
// parser), so a report with fewer than MIN_MODULES modules is an error in
// every mode. A gate that measured nothing must not read as clean.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { fileURLToPath } from "node:url";

export const BASELINE_FILE = "boundaries-baseline.json";
export const MIN_MODULES = 20;

export function refreshCommand() {
  return "node scripts/boundaries-ratchet.mjs --write";
}

/** Turn dependency-cruiser's JSON report into sorted `<rule>:<from>:<to>` keys. */
export function parse(report) {
  const { summary } = report;
  if (!summary || (summary.totalCruised ?? 0) < MIN_MODULES) {
    throw new Error(
      `dependency-cruiser cruised ${summary?.totalCruised ?? 0} module(s), expected at least ${MIN_MODULES}: nothing was measured`,
    );
  }
  return [...new Set(summary.violations.map((v) => `${v.rule.name}:${v.from}:${v.to}`))].sort();
}

/** Compare current keys with the baseline. Pure, so the decision is testable. */
export function compare(baseline, current) {
  const was = new Set(baseline);
  const now = new Set(current);
  return {
    added: current.filter((k) => !was.has(k)),
    stale: baseline.filter((k) => !now.has(k)),
  };
}

function runDepcruise() {
  const run = (cmd, args) =>
    execFileSync(cmd, args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] });
  const args = ["depcruise", "src", "--config", ".dependency-cruiser.cjs", "--output-type", "json"];
  try {
    return JSON.parse(run("pnpm", ["exec", ...args]));
  } catch (error) {
    // depcruise exits non-zero when it finds violations; the report is on stdout.
    if (error.stdout) return JSON.parse(error.stdout);
    throw error;
  }
}

function main(argv) {
  const write = argv.includes("--write");
  const enforce = argv.includes("--enforce");
  const current = parse(runDepcruise());

  if (write) {
    fs.writeFileSync(BASELINE_FILE, `${JSON.stringify({ violations: current }, null, 2)}\n`);
    console.log(`boundaries: wrote ${current.length} violation(s) to ${BASELINE_FILE}`);
    return 0;
  }

  const baseline = fs.existsSync(BASELINE_FILE)
    ? JSON.parse(fs.readFileSync(BASELINE_FILE, "utf8")).violations
    : [];
  const { added, stale } = compare(baseline, current);
  console.log(
    `boundaries: ${current.length} violation(s), ${baseline.length} baselined, ${enforce ? "enforcing" : "report only"}.`,
  );
  const level = enforce ? "error" : "warning";
  for (const key of added) console.log(`::${level}::${key} is a layer violation not in the baseline. Move the import.`);
  for (const key of stale) {
    console.log(`::${level}::${key} is baselined but gone. Refresh the baseline: ${refreshCommand()}`);
  }
  const problems = added.length + stale.length;
  if (problems > 0 && enforce) {
    console.log(`boundaries: FAILED (${problems} problem(s))`);
    return 1;
  }
  console.log(problems > 0 ? "boundaries: report only, not failing" : "boundaries: OK");
  return 0;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (error) {
    console.error(`boundaries: ${error.message}`);
    process.exit(1);
  }
}
