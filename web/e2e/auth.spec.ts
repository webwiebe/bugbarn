/**
 * BugBarn auth round-trip tests — login and server-driven logout.
 *
 * These tests require a running BugBarn instance reachable at
 * BUGBARN_TEST_URL (default http://localhost:8080) with local admin
 * credentials configured:
 *
 *   BUGBARN_TEST_USER  (default: admin)
 *   BUGBARN_TEST_PASS  (default: password)
 *
 * Against an OIDC-enabled deployment the logout test additionally verifies
 * the browser is sent to the IdP end-session endpoint (the server returns
 * logout_url with id_token_hint), completing RP-initiated logout.
 */

import type { Page } from "@playwright/test";
import { test, expect } from "./fixtures";
import { loginIfRequired } from "./login-helper";
import { EMPTY_STATE } from "./global-setup";

// These tests sign in on the form, so they start without the shared session.
test.use({ storageState: EMPTY_STATE });

async function login(page: Page): Promise<boolean> {
  await page.goto("./");
  return loginIfRequired(page);
}

test("login issues an opaque server-side session", async ({ page, context }) => {
  const authRequired = await login(page);
  test.skip(!authRequired, "auth not enabled on this instance");

  const cookies = await context.cookies();
  const session = cookies.find((c) => c.name === "bugbarn_session");
  expect(session).toBeTruthy();
  expect(session!.httpOnly).toBe(true);
  // Opaque handle, not a claims-carrying "payload.signature" token.
  expect(session!.value).not.toContain(".");

  // The session authenticates /api/v1/me.
  const me = await page.request.get("/api/v1/me");
  expect(me.ok()).toBe(true);
  const payload = (await me.json()) as { authenticated?: boolean; username?: string };
  expect(payload.authenticated).toBe(true);
});

test("logout kills the session server-side and follows logout_url", async ({ page, context }) => {
  const authRequired = await login(page);
  test.skip(!authRequired, "auth not enabled on this instance");

  const wasOIDC = (await context.cookies()).some(
    (c) => c.name === "bugbarn_auth_method" && c.value === "oidc",
  );

  // Open the avatar menu and log out.
  await page.locator("#user-avatar-btn").click();
  await page.locator("#bb-logout").click();

  if (wasOIDC) {
    // Server-driven RP-initiated logout: the SPA follows logout_url to the
    // IdP end-session endpoint.
    await page.waitForURL(/end-session|logged-out|\/$/, { timeout: 10_000 });
  } else {
    await expect(page.locator("#login-form")).toBeVisible({ timeout: 10_000 });
  }

  // The old session no longer authenticates (row deleted server-side).
  const me = await page.request.get("/api/v1/me");
  expect(me.status()).toBe(401);
});
