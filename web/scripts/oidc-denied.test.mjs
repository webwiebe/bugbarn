// Unit test for the text of the refused sign-in card.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { oidcDeniedMessage } from "../src/oidc-denied.ts";

test("a token from another organization tells the user to switch", () => {
  const msg = oidcDeniedMessage("wrong_organization", "alice@example.com");
  assert.match(msg, /as alice@example\.com/);
  assert.match(msg, /Switch to your home organization in IAMBarn, then sign in again/);
});

test("any other refusal says the account has no access", () => {
  assert.equal(
    oidcDeniedMessage("access_denied", ""),
    "You're signed in to IAMBarn, but that account doesn't have access to BugBarn.",
  );
});
