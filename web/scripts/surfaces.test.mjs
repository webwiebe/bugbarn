// Unit test for the rows of the issues page "By surface" panel.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { surfaceLink, surfaceRows, surfacesApply } from "../src/surfaces.ts";

const row = (surface, d, w, open) => ({ surface, issues_24h: d, issues_7d: w, open });

test("surfaceRows sorts busiest first and drops blank surfaces", () => {
  const rows = surfaceRows({ surfaces: [row("mcp", 0, 1, 0), row("", 5, 5, 5), row("wordpress", 2, 4, 3), row("lightroom", 1, 4, 1)] });
  assert.deepEqual(rows.map((r) => r.surface), ["lightroom", "wordpress", "mcp"]);
});

test("surfaceRows tolerates a missing or malformed payload", () => {
  assert.deepEqual(surfaceRows(null), []);
  assert.deepEqual(surfaceRows({}), []);
  assert.deepEqual(surfaceRows({ surfaces: "nope" }), []);
});

test("surfacesApply only for a single project", () => {
  assert.equal(surfacesApply("profotograaf", null), true);
  assert.equal(surfacesApply("__all", null), false);
  assert.equal(surfacesApply("default", null), false);
  assert.equal(surfacesApply("profotograaf", "team"), false);
});

test("surfaceLink fills {surface} with the encoded name", () => {
  assert.equal(
    surfaceLink("https://funnelbarn.example/p/x?surface={surface}", "word press"),
    "https://funnelbarn.example/p/x?surface=word%20press",
  );
});

test("surfaceLink uses a template without a placeholder as it is", () => {
  assert.equal(surfaceLink("https://funnelbarn.example/p/x/funnels", "mcp"), "https://funnelbarn.example/p/x/funnels");
});

test("surfaceLink gives no link for an empty or unsafe setting", () => {
  assert.equal(surfaceLink("", "mcp"), null);
  assert.equal(surfaceLink(undefined, "mcp"), null);
  assert.equal(surfaceLink("   ", "mcp"), null);
  assert.equal(surfaceLink("javascript:alert({surface})", "mcp"), null);
  assert.equal(surfaceLink("/relative/{surface}", "mcp"), null);
});
