// DOM test for the Settings project filter. It loads the real stylesheet, so
// the assertions are on computed style: a hidden row that a class keeps
// displayed fails here.

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";

const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");

const dom = new JSDOM(`<!doctype html><html><head><style>${css}</style></head><body>
<input id="project-filter"><select id="project-sort"><option value="default"></option></select>
<select id="project-status-filter"><option value="all"></option><option value="pending"></option><option value="active"></option></select>
<div class="wrap"><div id="project-list">
  <div class="project-row" data-name="videoslop" data-slug="videoslop" data-status="active"></div>
  <div class="project-row" data-name="bugbarn" data-slug="bugbarn-service" data-status="active"></div>
  <div class="project-row" data-name="brandtrace" data-slug="brandtrace" data-status="pending"></div>
</div></div></body></html>`);
globalThis.document = dom.window.document;

const { wireProjectList, snapshotProjectList, restoreProjectList } = await import("../src/project-filter.ts");

const doc = dom.window.document;
const list = doc.getElementById("project-list");
const filter = doc.getElementById("project-filter");
const status = doc.getElementById("project-status-filter");
wireProjectList({ list, filter, sort: doc.getElementById("project-sort"), status });

const shown = (el) => dom.window.getComputedStyle(el).display !== "none";
const visibleRows = () => [...list.querySelectorAll(".project-row")].filter(shown).map((r) => r.dataset.name);
const type = (value) => {
  filter.value = value;
  filter.dispatchEvent(new dom.window.Event("input"));
};
const empty = () => doc.getElementById("project-empty");

test("a row shows as flex before any filter is applied", () => {
  assert.equal(dom.window.getComputedStyle(list.querySelector(".project-row")).display, "flex");
});

test("a partial match leaves only the matching rows visible", () => {
  type("video");
  assert.deepEqual(visibleRows(), ["videoslop"]);
  assert.equal(shown(empty()), false);
});

test("a query that matches nothing hides every row and shows the empty state", () => {
  type("zzz");
  assert.deepEqual(visibleRows(), []);
  assert.equal(shown(empty()), true);
  assert.match(empty().textContent, /No projects match "zzz"/);
});

test("Clear empties the query and brings the rows back", () => {
  doc.getElementById("project-empty-clear").click();
  assert.equal(filter.value, "");
  assert.equal(visibleRows().length, 3);
  assert.equal(shown(empty()), false);
});

test("the status filter hides rows of other statuses", () => {
  status.value = "pending";
  status.dispatchEvent(new dom.window.Event("change"));
  assert.deepEqual(visibleRows(), ["brandtrace"]);
});

// Settings replaces its markup when loadSettings' fetches land. The controls
// the user set must come back on the new markup, with the empty state and focus.
test("a snapshot carries the controls and focus across a re-render", () => {
  status.value = "all";
  type("zzz");
  filter.focus();
  filter.setSelectionRange(1, 2);
  const snap = snapshotProjectList(doc.body);
  assert.deepEqual(snap, { filter: "zzz", sort: "default", status: "all", focused: true, selectionStart: 1, selectionEnd: 2 });

  doc.body.innerHTML = `<input id="project-filter"><select id="project-sort"><option value="default"></option></select>
<select id="project-status-filter"><option value="all"></option><option value="active"></option></select>
<div class="wrap"><div id="project-list">
  <div class="project-row" data-name="videoslop" data-slug="videoslop" data-status="active"></div>
</div></div>`;
  const fresh = {
    list: doc.getElementById("project-list"),
    filter: doc.getElementById("project-filter"),
    sort: doc.getElementById("project-sort"),
    status: doc.getElementById("project-status-filter"),
  };
  wireProjectList(fresh);
  restoreProjectList(fresh, snap);

  assert.equal(fresh.filter.value, "zzz");
  assert.equal(doc.activeElement, fresh.filter);
  assert.deepEqual([fresh.filter.selectionStart, fresh.filter.selectionEnd], [1, 2]);
  assert.equal(shown(fresh.list.querySelector(".project-row")), false);
  assert.match(doc.getElementById("project-empty").textContent, /No projects match "zzz"/);
});

test("no snapshot is taken when the controls are not on screen", () => {
  assert.equal(snapshotProjectList(new JSDOM("<p></p>").window.document.body), null);
});
