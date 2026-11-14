#!/usr/bin/env node
// Line coverage of the web app (all of web/src, files no test loads count as
// 0%), as a ratchet. Same rules as scripts/check-coverage.sh for the Go code:
//
//   total < MINIMUM                 -> FAIL, below the fixed 80% line floor
//   total < baseline                -> FAIL, coverage dropped
//   total > baseline + MAX_DRIFT    -> FAIL, the baseline is beatable and must
//                                      be raised in the same commit
//
// The baseline lives in coverage-baseline.txt and only goes up. MAX_DRIFT is
// 3.0 so a run on a loaded runner cannot fail at random: the Go measurement
// moves by 1.2pp between an idle and a loaded machine, and the baseline sits
// at least 1.0pp under the measurement.
//
// Measurement is c8 (V8 coverage) with --all, so a source file that no test
// imports is read from disk and reported with every line uncovered.
//
// `--write` prints the baseline that fits the current measurement.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const BASELINE_FILE = "coverage-baseline.txt";
export const MINIMUM = 80;
export const MAX_DRIFT = 3.0;
const REPORT_DIR = "coverage";

/** The baseline that locks in a measurement: one point under it, as for Go. */
export function suggestedBaseline(total) {
  return (Math.floor(total * 10) / 10 - 1.0).toFixed(1);
}

/**
 * The whole decision. Pure, so the gate can be tested without running c8.
 * Returns { ok, failures } where each failure names what to do.
 */
export function evaluate({ total, baseline, minimum = MINIMUM, maxDrift = MAX_DRIFT }) {
  const failures = [];
  if (!Number.isFinite(total)) {
    return { ok: false, failures: ["could not read a coverage total, the test run did not produce one"] };
  }
  if (!Number.isFinite(baseline)) {
    return { ok: false, failures: [`the baseline is not a number, write one with: echo ${suggestedBaseline(total)} > ${BASELINE_FILE}`] };
  }
  if (total < minimum) {
    failures.push(`line coverage ${total}% is below the fixed minimum of ${minimum}%`);
  }
  if (total < baseline) {
    failures.push(`line coverage ${total}% dropped below the baseline of ${baseline}%`);
  }
  if (total > baseline + maxDrift) {
    failures.push(
      `line coverage ${total}% is more than ${maxDrift}pp above the baseline of ${baseline}%, so that gain is unprotected. Raise the floor in this same commit: echo ${suggestedBaseline(total)} > ${BASELINE_FILE}`,
    );
  }
  return { ok: failures.length === 0, failures };
}

/** Reads the line percentage and the per-file percentages from c8's json-summary. */
export function readSummary(summary) {
  const total = summary?.total?.lines?.pct;
  const files = Object.entries(summary ?? {})
    .filter(([name]) => name !== "total")
    .map(([name, entry]) => ({ name, pct: entry.lines.pct }));
  return { total: typeof total === "number" ? total : Number.NaN, files };
}

/** Files under the minimum, worst first. */
export function filesBelow(files, minimum = MINIMUM) {
  return files.filter((f) => f.pct < minimum).sort((a, b) => a.pct - b.pct);
}

function measure(webDir) {
  const c8 = path.join(webDir, "node_modules", ".bin", "c8");
  execFileSync(
    c8,
    [
      "--all",
      "--src=src",
      "--include=src/**/*.ts",
      "--exclude=src/types.ts",
      "--reporter=json-summary",
      "--reporter=text-summary",
      `--reports-dir=${REPORT_DIR}`,
      "--clean",
      process.execPath,
      "--test",
      ...fs.readdirSync(path.join(webDir, "scripts")).filter((f) => f.endsWith(".test.mjs")).map((f) => `scripts/${f}`),
    ],
    { cwd: webDir, stdio: ["ignore", "inherit", "inherit"] },
  );
  return readSummary(JSON.parse(fs.readFileSync(path.join(webDir, REPORT_DIR, "coverage-summary.json"), "utf8")));
}

function main() {
  const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const { total, files } = measure(webDir);
  console.log(`Total web line coverage: ${total}%`);

  if (process.argv.includes("--write")) {
    console.log(`baseline for this measurement: ${suggestedBaseline(total)}`);
    return;
  }

  const baselinePath = path.join(webDir, BASELINE_FILE);
  const baseline = fs.existsSync(baselinePath) ? Number(fs.readFileSync(baselinePath, "utf8").trim()) : Number.NaN;
  const res = evaluate({ total, baseline });
  const below = filesBelow(files);
  if (below.length) {
    console.log("files under the minimum:");
    for (const f of below) console.log(`  ${f.pct}%  ${f.name}`);
  }
  if (!res.ok) {
    for (const failure of res.failures) console.error(`FAIL: ${failure}`);
    process.exit(1);
  }
  console.log(`coverage: OK (${total}% within [${baseline}%, ${baseline + MAX_DRIFT}%], minimum ${MINIMUM}%)`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main();
}
