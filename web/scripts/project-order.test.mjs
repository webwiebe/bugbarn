// Unit test for the project switcher's order, search and favourites.
// Node strips the types when it loads the .ts source directly.

import "./support/dom-env.mjs";
import test from "node:test";
import assert from "node:assert/strict";
const { compareByName, matchesSearch, orderProjects, parseFavourites, toggleFavourite } =
  await import("../src/components/project-order.ts");

const p = (slug, name = slug) => ({ slug, name });
const slugs = (list) => list.map((x) => x.slug);

test("projects sort by name, ignoring case", () => {
  const { others } = orderProjects([p("c", "cherry"), p("a", "Banana"), p("b", "apple")], new Set(), "");
  assert.deepEqual(slugs(others), ["b", "a", "c"]);
});

test("equal names fall back to the slug", () => {
  assert.ok(compareByName(p("a", "Same"), p("b", "Same")) < 0);
});

test("search matches the name or the slug, ignoring case and spaces around it", () => {
  assert.ok(matchesSearch(p("rr-backend", "Rapid Root"), " ROOT "));
  assert.ok(matchesSearch(p("rr-backend", "Rapid Root"), "backend"));
  assert.ok(!matchesSearch(p("rr-backend", "Rapid Root"), "videoslop"));
  assert.ok(matchesSearch(p("x"), ""));
});

test("starred projects come first, each group alphabetical", () => {
  const all = [p("d", "Delta"), p("b", "Bravo"), p("a", "Alpha"), p("c", "Charlie")];
  const { favourites, others } = orderProjects(all, new Set(["d", "b"]), "");
  assert.deepEqual(slugs(favourites), ["b", "d"]);
  assert.deepEqual(slugs(others), ["a", "c"]);
});

test("the search filters both groups", () => {
  const all = [p("alpha"), p("beta"), p("alps")];
  const { favourites, others } = orderProjects(all, new Set(["beta", "alps"]), "al");
  assert.deepEqual(slugs(favourites), ["alps"]);
  assert.deepEqual(slugs(others), ["alpha"]);
  const none = orderProjects(all, new Set(), "zzz");
  assert.equal(none.favourites.length + none.others.length, 0);
});

test("ordering leaves the input untouched", () => {
  const all = [p("b"), p("a")];
  orderProjects(all, new Set(), "");
  assert.deepEqual(slugs(all), ["b", "a"]);
});

test("toggling adds a slug and removes it again", () => {
  assert.deepEqual(toggleFavourite(["a"], "b"), ["a", "b"]);
  assert.deepEqual(toggleFavourite(["a", "b"], "a"), ["b"]);
});

test("stored favourites parse to a list of strings, junk to an empty list", () => {
  assert.deepEqual(parseFavourites('["a","b"]'), ["a", "b"]);
  assert.deepEqual(parseFavourites('["a",3,null]'), ["a"]);
  assert.deepEqual(parseFavourites('{"a":1}'), []);
  assert.deepEqual(parseFavourites("not json"), []);
  assert.deepEqual(parseFavourites(null), []);
});
