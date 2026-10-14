import { expect, type Page } from "@playwright/test";

const TEST_USER = process.env["BUGBARN_TEST_USER"] ?? "admin";
const TEST_PASS = process.env["BUGBARN_TEST_PASS"] ?? "password";

/** Signs in when the app shows the login form; returns at once when auth is off. */
export async function loginIfRequired(page: Page): Promise<void> {
  const loginForm = page.locator("#login-form");
  await page.locator("#login-form, #issue-list").first().waitFor({ timeout: 8_000 });
  if (!(await loginForm.isVisible().catch(() => false))) return;
  await loginForm.locator('input[name="username"]').fill(TEST_USER);
  await loginForm.locator('input[name="password"]').fill(TEST_PASS);
  await loginForm.locator('button[type="submit"]').click();
  await expect(loginForm).not.toBeVisible({ timeout: 8_000 });
}
