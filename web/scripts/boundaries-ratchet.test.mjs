// The boundaries ratchet's own test, in two halves. `parse` and `compare` are the
// decision, tested on recorded reports. The rules themselves are proven against
// real files: each one runs the real dependency-cruiser over a throwaway tree, so a
// rule that has quietly stopped matching (or a parser that cruises nothing) fails here.

import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { compare, parse, MIN_MODULES } from "./boundaries-ratchet.mjs";

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CONFIG = path.join(WEB, ".dependency-cruiser.cjs");
const DEPCRUISE = path.join(WEB, "node_modules/.bin/depcruise");

function report(violations, totalCruised = MIN_MODULES) {
  return {
    summary: {
      totalCruised,
      violations: violations.map(([rule, from, to]) => ({ rule: { name: rule }, from, to })),
    },
  };
}

test("parse builds sorted rule:from:to keys", () => {
  const keys = parse(report([["b-rule", "src/b.ts", "src/a.ts"], ["a-rule", "src/a.ts", "src/b.ts"]]));
  assert.deepEqual(keys, ["a-rule:src/a.ts:src/b.ts", "b-rule:src/b.ts:src/a.ts"]);
});

test("a report that cruised almost nothing is an error, not a clean tree", () => {
  assert.throws(() => parse(report([], 0)), /nothing was measured/);
});

test("a new violation is added, a gone one is stale, an unchanged one is neither", () => {
  const result = compare(["r:a:b", "r:c:d"], ["r:a:b", "r:e:f"]);
  assert.deepEqual(result.added, ["r:e:f"]);
  assert.deepEqual(result.stale, ["r:c:d"]);
});

/** Writes the files under a throwaway src/ and returns the rule names that fired. */
function violatedRules(files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "boundaries-"));
  try {
    fs.writeFileSync(path.join(root, "tsconfig.json"), JSON.stringify({ compilerOptions: { module: "ES2022" } }));
    // Padding keeps the tree above MIN_MODULES, as the real one is.
    for (let i = 0; i < MIN_MODULES; i++) files[`src/pad${i}.ts`] = "export const p = 1;\n";
    for (const [file, body] of Object.entries(files)) {
      fs.mkdirSync(path.dirname(path.join(root, file)), { recursive: true });
      fs.writeFileSync(path.join(root, file), body);
    }
    let out;
    try {
      out = execFileSync(DEPCRUISE, ["src", "--config", CONFIG, "--output-type", "json"], { cwd: root, encoding: "utf8" });
    } catch (error) {
      out = error.stdout;
    }
    const { summary } = JSON.parse(out);
    assert.ok(summary.totalCruised >= MIN_MODULES, "the fixture tree must be cruised");
    return [...new Set(summary.violations.map((v) => v.rule.name))].sort();
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

test("a tree that respects the layers reports nothing", () => {
  assert.deepEqual(
    violatedRules({
      "src/types.ts": "export type T = number;\n",
      "src/format.ts": 'import type { T } from "./types.js";\nexport const f = (n: T) => String(n);\n',
      "src/components/chip.ts": 'import { f } from "../format.js";\nexport const chip = f(1);\n',
      "src/views-a.ts": 'import { chip } from "./components/chip.js";\nexport const a = chip;\n',
      "src/bootstrap.ts": 'import { a } from "./views-a.js";\nexport const b = a;\n',
    }),
    [],
  );
});

test("the shared bottom layer importing a view is flagged", () => {
  assert.deepEqual(
    violatedRules({
      "src/views-a.ts": "export const a = 1;\n",
      "src/format.ts": 'import { a } from "./views-a.js";\nexport const f = a;\n',
    }),
    ["bottom-layer-imports-nothing-above"],
  );
});

test("a component importing the http layer is flagged", () => {
  assert.deepEqual(
    violatedRules({
      "src/http.ts": "export const h = 1;\n",
      "src/components/chip.ts": 'import { h } from "../http.js";\nexport const chip = h;\n',
    }),
    ["components-stay-presentational"],
  );
});

test("a view importing an entrypoint is flagged", () => {
  assert.deepEqual(
    violatedRules({
      "src/bootstrap.ts": "export const b = 1;\n",
      "src/views-a.ts": 'import { b } from "./bootstrap.js";\nexport const a = b;\n',
    }),
    ["only-entrypoints-import-the-entrypoints"],
  );
});

test("a circular import is flagged", () => {
  assert.deepEqual(
    violatedRules({
      "src/views-a.ts": 'import { b } from "./views-b.js";\nexport const a = b;\n',
      "src/views-b.ts": 'import { a } from "./views-a.js";\nexport const b = a;\n',
    }),
    ["no-circular"],
  );
});

test("production code importing a test is flagged", () => {
  assert.deepEqual(
    violatedRules({
      "src/helper.test.ts": "export const h = 1;\n",
      "src/views-a.ts": 'import { h } from "./helper.test.js";\nexport const a = h;\n',
    }),
    ["production-code-does-not-import-tests"],
  );
});
