#!/usr/bin/env node
// knip (dead code in the web app), as a ratchet. Unused files, exports, types
// and dependencies are counted per item and compared with knip-baseline.json.
//
// Same shape as oxlint-ratchet.mjs: the gate is symmetric. An item that is not
// in the baseline fails (new dead code), and so does a baseline item that is
// gone (stale allowance: it leaves room for that item to come back with the
// gate still green). The failure prints the refresh command.
//
// `--write` refreshes the baseline after a deliberate change.
// `--report-only` prints the same report and always exits 0. CI runs this mode
// while the gate soaks on real pull requests; drop the flag to enforce.
//
// Items are keyed `type:file:name`, without line numbers, so moving code inside
// a file does not read as one finding gone and another new.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const BASELINE_FILE = "knip-baseline.json";

// The issue types knip reports that mean "this is unused". `duplicates` and
// `enumMembers` are left out on purpose: they are style findings, not dead code.
export const ITEM_TYPES = [
  "dependencies",
  "devDependencies",
  "optionalPeerDependencies",
  "unlisted",
  "binaries",
  "unresolved",
  "exports",
  "types",
];

/** The command that refreshes the baseline, printed verbatim on every failure. */
export function refreshCommand() {
  return "node scripts/knip-ratchet.mjs --write";
}

/**
 * Flatten knip's JSON report to a sorted list of unique item keys.
 * Pure, so the gate can be tested without running knip.
 */
export function flatten(report) {
  const keys = new Set();
  for (const file of report.files ?? []) keys.add(`files:${file}:`);
  for (const issue of report.issues ?? []) {
    // knip's json reporter lists an unused file as an issue of its own.
    if ((issue.files ?? []).length > 0) keys.add(`files:${issue.file}:`);
    for (const type of ITEM_TYPES) {
      for (const item of issue[type] ?? []) {
        keys.add(`${type}:${issue.file}:${item.name}`);
      }
    }
  }
  return [...keys].sort();
}

/**
 * Compare the current items with the baseline.
 *
 *   - an item not in the baseline  -> added (new dead code)
 *   - a baseline item no longer found -> removed (stale allowance)
 *
 * Both fail, so `ok` requires neither.
 */
export function compare(baselineItems, currentItems) {
  const was = new Set(baselineItems);
  const now = new Set(currentItems);
  const added = [...now].filter((k) => !was.has(k)).sort();
  const removed = [...was].filter((k) => !now.has(k)).sort();
  return {
    ok: added.length === 0 && removed.length === 0,
    added,
    removed,
    total: now.size,
    baselineTotal: was.size,
  };
}

function runKnip(webDir) {
  let raw = "";
  try {
    raw = execFileSync("npx", ["knip", "--reporter", "json", "--no-exit-code"], {
      cwd: webDir,
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
    });
  } catch (err) {
    if (!err.stdout) throw new Error(`knip did not run: ${err.message}`);
    raw = err.stdout;
  }
  // Plugins that load app config (dotenv via the Playwright config) can print a
  // banner to stdout before the report; the report is the line that opens a JSON object.
  const start = raw.search(/^\{/m);
  return JSON.parse(start > 0 ? raw.slice(start) : raw);
}

function main() {
  const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const baselinePath = path.join(webDir, BASELINE_FILE);
  const write = process.argv.includes("--write");
  const reportOnly = process.argv.includes("--report-only");

  const items = flatten(runKnip(webDir));

  if (write) {
    fs.writeFileSync(baselinePath, `${JSON.stringify({ total: items.length, items }, null, 2)}\n`);
    console.log(`knip baseline written: ${items.length} items`);
    return;
  }

  const baseline = fs.existsSync(baselinePath)
    ? JSON.parse(fs.readFileSync(baselinePath, "utf8"))
    : { total: 0, items: [] };
  const res = compare(baseline.items, items);

  if (res.ok && res.total === 0) {
    console.log("knip: clean");
    return;
  }
  console.log(`knip: ${res.total} items (baseline ${res.baselineTotal})`);
  if (res.ok) {
    console.log("knip: on the baseline, exactly.");
    return;
  }

  const say = reportOnly ? console.log : console.error;
  say("");
  say(
    reportOnly
      ? "knip ratchet: the tree and the baseline disagree, but this run is report-only."
      : "knip ratchet FAILED: the tree and the baseline disagree.",
  );
  if (res.added.length) {
    say("");
    say("Dead code that is not in the baseline:");
    for (const k of res.added) say(`  ${k}`);
    say("");
    say("Delete it, or stop exporting it if only its own file uses it.");
  }
  if (res.removed.length) {
    say("");
    say("Baseline items that are gone, so the baseline is now stale:");
    for (const k of res.removed) say(`  ${k}`);
    say("");
    say("The baseline has to follow the work down. Lock the improvement in:");
    say(`  ${refreshCommand()}`);
  }

  if (reportOnly) {
    console.log("");
    console.log("knip ratchet: report-only run, not gating.");
    return;
  }
  process.exit(1);
}

// Only run when invoked directly, so the test can import the pure helpers.
if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  main();
}
