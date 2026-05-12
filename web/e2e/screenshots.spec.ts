import { test, expect, type Page } from "@playwright/test";
import path from "path";

const TEST_USER = process.env["BUGBARN_TEST_USER"] ?? "admin";
const TEST_PASS = process.env["BUGBARN_TEST_PASS"] ?? "password";

const SCREENSHOT_DIR = path.join(import.meta.dirname, "..", "screenshots");

async function login(page: Page): Promise<void> {
  await page.goto("/");
  const loginForm = page.locator("#login-form");
  try {
    await loginForm.waitFor({ state: "visible", timeout: 5_000 });
  } catch {
    return;
  }

  await loginForm.locator('input[name="username"]').fill(TEST_USER);
  await loginForm.locator('input[name="password"]').fill(TEST_PASS);
  await loginForm.locator('button[type="submit"]').click();
  await expect(loginForm).not.toBeVisible({ timeout: 8_000 });
  await page.waitForTimeout(1_000);
}

async function navigateTo(page: Page, route: string): Promise<void> {
  await page.evaluate((r) => { location.hash = `#/${r}`; }, route);
  await page.waitForTimeout(1_500);
}

async function snap(page: Page, route: string, name: string, projectName: string): Promise<void> {
  await navigateTo(page, route);
  await page.screenshot({
    path: path.join(SCREENSHOT_DIR, projectName, `${name}.png`),
    fullPage: true,
  });
}

test("capture all pages", async ({ page }, testInfo) => {
  const proj = testInfo.project.name;
  await login(page);

  // Issues list
  await snap(page, "issues", "issues", proj);

  // Issue detail — click first issue and wait for content to load
  const firstIssue = page.locator("#issue-list .issue-row").first();
  if (await firstIssue.isVisible().catch(() => false)) {
    await firstIssue.click();
    try {
      await page.locator(".issue-hero").waitFor({ state: "visible", timeout: 5_000 });
    } catch {
      // fall through — screenshot whatever state we have
    }
    await page.waitForTimeout(500);
    await page.screenshot({
      path: path.join(SCREENSHOT_DIR, proj, "issue-detail.png"),
      fullPage: true,
    });
  }

  // Navigate back to issues list before going to other sections,
  // so the detail-view doesn't bleed into subsequent pages.
  await navigateTo(page, "issues");

  // Releases
  await snap(page, "releases", "releases", proj);

  // Release detail
  const firstRelease = page.locator(".release-row").first();
  if (await firstRelease.isVisible().catch(() => false)) {
    await firstRelease.click();
    await page.waitForTimeout(800);
    await page.screenshot({
      path: path.join(SCREENSHOT_DIR, proj, "release-detail.png"),
      fullPage: true,
    });
  }

  // Alerts
  await snap(page, "alerts", "alerts", proj);

  // Logs
  await snap(page, "logs", "logs", proj);

  // Settings
  await snap(page, "settings", "settings", proj);
});
