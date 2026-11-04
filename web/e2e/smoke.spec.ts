/**
 * BugBarn smoke tests — end-to-end integration tests.
 *
 * These tests require a running BugBarn instance reachable at http://localhost:8080.
 * Start the server before running:
 *
 *   pnpm run test:e2e
 *
 * Credentials are read from environment variables:
 *   BUGBARN_TEST_USER  (default: admin)
 *   BUGBARN_TEST_PASS  (default: password)
 */

import { test, expect } from "./fixtures";
import { loginIfRequired } from "./login-helper";
import { EMPTY_STATE } from "./global-setup";

// ---------------------------------------------------------------------------
// Test: login flow
// ---------------------------------------------------------------------------

test.describe("signed out", () => {
  // This test signs in on the form, so it starts without the shared session.
  test.use({ storageState: EMPTY_STATE });

  test("login flow", async ({ page }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await page.goto("./");

    // When auth is required the app shows #login-form on its login screen and
    // hides the app frame; signing in brings the issue list back. Without auth the
    // issue list is on screen at once.
    await loginIfRequired(page);
    await expect(page.locator("#issue-list")).toBeVisible({ timeout: 8_000 });

    // The URL should stay at / (single-page app, no redirect to /login).
    expect(page.url()).not.toContain("/login");

    expect(pageErrors).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// Test: issue list loads
// ---------------------------------------------------------------------------

test("issue list loads", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await page.goto("./");
  await loginIfRequired(page);

  // Navigate to the issues view (hash-based routing).
  await page.goto("./#/issues");

  // The overview-view must not be hidden and must contain #issue-list.
  const overviewView = page.locator("#overview-view");
  await expect(overviewView).toBeVisible({ timeout: 8_000 });
  await expect(overviewView).not.toHaveClass(/hidden/);

  const issueList = page.locator("#issue-list");
  await expect(issueList).toBeVisible();

  // The issue list should have at least the container present — either a list
  // of .issue-row buttons, an .empty placeholder, or an .error message.  Any
  // of these means the list rendered without a JS crash.
  await expect(
    issueList.locator(".issue-row, .empty, .error").first()
  ).toBeVisible({ timeout: 8_000 });

  expect(pageErrors).toHaveLength(0);
});

// ---------------------------------------------------------------------------
// Test: issue detail navigation
// ---------------------------------------------------------------------------

test("issue detail navigation", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await page.goto("./");
  await loginIfRequired(page);

  // Check the issues API to see whether any issues exist.
  const response = await page.request.get("/api/v1/issues");
  if (!response.ok()) {
    test.skip();
    return;
  }

  const body = await response.json() as Record<string, unknown>;
  // The API returns { issues: [...] } or a top-level array.
  const issues = Array.isArray(body) ? body : (Array.isArray(body["issues"]) ? body["issues"] as unknown[] : []);

  if (!issues.length) {
    test.skip();
    return;
  }

  // Pick the first issue and extract its id (handles id/ID/issueId fields).
  const first = issues[0] as Record<string, unknown>;
  const issueId = String(
    first["id"] ?? first["ID"] ?? first["issueId"] ?? first["IssueID"] ?? first["issue_id"] ?? ""
  );

  if (!issueId) {
    test.skip();
    return;
  }

  // Navigate to the issue detail via the hash router.
  await page.goto(`./#/issues/${encodeURIComponent(issueId)}`);

  // The detail-view should become visible (not hidden) and contain the issue
  // hero section with an h3 title.
  const detailView = page.locator("#detail-view");
  await expect(detailView).toBeVisible({ timeout: 8_000 });
  await expect(detailView).not.toHaveClass(/hidden/);

  // The issue hero block contains an h3 with the issue title.
  const issueHero = detailView.locator(".issue-hero");
  await expect(issueHero).toBeVisible({ timeout: 8_000 });

  const heroTitle = issueHero.locator("h3");
  await expect(heroTitle).toBeVisible();
  await expect(heroTitle).not.toBeEmpty();

  expect(pageErrors).toHaveLength(0);
});

// ---------------------------------------------------------------------------
// Test: live events page
// ---------------------------------------------------------------------------

test("live events page", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  // The stylesheet hides the live panel at 1120px and narrower.
  test.skip((page.viewportSize()?.width ?? 0) <= 1120, "the live panel is hidden at this width");

  await page.goto("./");
  await loginIfRequired(page);

  // The live events panel (#panel-live aside) is always present in the DOM
  // alongside the main panel — it is not a separate route.
  const liveList = page.locator("#live-list");
  await expect(liveList).toBeVisible({ timeout: 8_000 });

  // The status indicator (#live-status chip) must be present.  Once the
  // EventSource connects it reads "Connected"; while reconnecting it reads
  // "Reconnecting".  Either way the element must exist and be non-empty.
  const liveStatus = page.locator("#live-status");
  await expect(liveStatus).toBeVisible({ timeout: 8_000 });

  // Wait until the status chip shows something meaningful (the app populates
  // it as soon as the EventSource fires open or error).
  await expect(liveStatus).not.toBeEmpty({ timeout: 8_000 });

  // The live panel should render either event buttons or an .empty placeholder.
  // Give the stream a moment to fire at least one message or confirm no events.
  await expect(
    liveList.locator(".item, .empty").first()
  ).toBeVisible({ timeout: 8_000 });

  expect(pageErrors).toHaveLength(0);
});
