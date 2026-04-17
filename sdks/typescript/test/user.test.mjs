import assert from "node:assert/strict";
import test from "node:test";

// Import directly from the built ESM output, same as the main test file.
import { setUser, clearUser } from "../dist/esm/index.js";
// getUser is not exported from the public API — we test via the built module directly.
import { getUser } from "../dist/esm/user.js";
// clearUser is re-exported; import clearBreadcrumbs for isolation
import { clearBreadcrumbs } from "../dist/esm/index.js";

test("setUser stores a copy of the user object", () => {
  const user = { id: "u1", email: "test@example.com", username: "tester" };
  setUser(user);
  const stored = getUser();
  assert.deepEqual(stored, user);
  // Mutating the original after setUser should not affect stored value
  user.email = "other@example.com";
  assert.equal(getUser()?.email, "test@example.com");
  clearUser();
});

test("getUser returns a copy, not the internal reference", () => {
  setUser({ id: "u2", username: "readonly" });
  const a = getUser();
  const b = getUser();
  assert.notStrictEqual(a, b);
  // Mutating one returned copy should not affect subsequent calls
  a.username = "mutated";
  assert.equal(getUser()?.username, "readonly");
  clearUser();
});

test("clearUser sets user back to null", () => {
  setUser({ id: "u3" });
  clearUser();
  assert.equal(getUser(), null);
});

test("getUser returns null when no user is set", () => {
  clearUser();
  assert.equal(getUser(), null);
});
