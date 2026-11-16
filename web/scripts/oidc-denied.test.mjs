// Unit test for the text of the refused sign-in card.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { oidcDeniedLinks, oidcDeniedMessage } from "../src/oidc-denied.ts";

const runtime = {
  loginURL: "/api/v1/oidc/login",
  switchAccountURL: "/api/v1/oidc/login?prompt=login",
  endSessionURL: "https://iam.example/end",
};

test("a token from another organization links to IAMBarn and back to sign-in", () => {
  const links = oidcDeniedLinks("wrong_organization", runtime, "https://iam.example", "https://bb.example");
  assert.deepEqual(links.map((l) => l.label), ["Open IAMBarn", "Sign in again", "Switch account", "Sign out of IAMBarn"]);
  assert.equal(links[0].href, "https://iam.example");
  assert.equal(links[1].href, "/api/v1/oidc/login");
  assert.equal(links[3].href, "https://iam.example/end?post_logout_redirect_uri=https%3A%2F%2Fbb.example%2F");
});

test("any other refusal keeps the account links only", () => {
  const links = oidcDeniedLinks("access_denied", runtime, "https://iam.example", "https://bb.example");
  assert.deepEqual(links.map((l) => l.label), ["Switch account", "Sign out of IAMBarn"]);
});

test("without runtime config there are no links", () => {
  assert.deepEqual(oidcDeniedLinks("wrong_organization", undefined, "", "https://bb.example"), []);
});

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
