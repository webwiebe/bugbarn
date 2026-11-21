// Unit test for the shared select option helper.
// Node strips the types when it loads the .ts source directly.

import "./support/dom-env.mjs";
import test from "node:test";
import assert from "node:assert/strict";
const { optionsHtml, sortedOptionsHtml, sortOptions } = await import("../src/components/select-options.ts");

const labels = (options) => options.map((o) => o.label);

test("options sort alphabetically, ignoring case", () => {
  const sorted = sortOptions([
    { value: "c", label: "cherry" }, { value: "a", label: "Banana" }, { value: "b", label: "apple" },
  ]);
  assert.deepEqual(labels(sorted), ["apple", "Banana", "cherry"]);
});

test("pinned options stay first in their given order", () => {
  const sorted = sortOptions([
    { value: "", label: "All environments" }, { value: "z", label: "zeta" }, { value: "a", label: "Alpha" },
  ], 1);
  assert.deepEqual(labels(sorted), ["All environments", "Alpha", "zeta"]);
});

test("numbers inside labels sort by value", () => {
  const sorted = sortOptions([{ value: "10", label: "Step 10" }, { value: "2", label: "Step 2" }]);
  assert.deepEqual(labels(sorted), ["Step 2", "Step 10"]);
});

test("sorting leaves the input untouched", () => {
  const input = [{ value: "b", label: "b" }, { value: "a", label: "a" }];
  sortOptions(input);
  assert.deepEqual(labels(input), ["b", "a"]);
});

test("the selected value is marked and values and labels are escaped", () => {
  const html = optionsHtml([{ value: 'a"b', label: "<x>" }, { value: "k", label: "K" }], "k");
  assert.equal(html, '<option value="a&quot;b">&lt;x&gt;</option><option value="k" selected>K</option>');
});

test("sortedOptionsHtml sorts and marks the selection in one step", () => {
  const html = sortedOptionsHtml([{ value: "z", label: "Zed" }, { value: "a", label: "Ay" }], "z");
  assert.equal(html, '<option value="a">Ay</option><option value="z" selected>Zed</option>');
});
