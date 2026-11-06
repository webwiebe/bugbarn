import { expect, type Page } from "@playwright/test";

const TEST_USER = process.env["BUGBARN_TEST_USER"] ?? "admin";
const TEST_PASS = process.env["BUGBARN_TEST_PASS"] ?? "password";

// The app renders one of these after /api/v1/me has answered: the login form when
// a session is needed, otherwise the issue list's rows, empty state or error.
const SETTLED = "#login-form:visible, #issue-list :is(.issue-row, .empty, .error)";

/**
 * Signs in when the app shows the login form and reports whether it did.
 *
 * It first waits for the app to settle. isVisible() does not wait, so a check
 * right after page.goto() reads the page before /api/v1/me has answered, finds
 * no form and takes the app for one without auth.
 */
export async function loginIfRequired(page: Page): Promise<boolean> {
  const loginForm = page.locator("#login-form");
  await page.locator(SETTLED).first().waitFor({ timeout: 15_000 });
  if (!(await loginForm.isVisible())) return false;
  await loginForm.locator('input[name="username"]').fill(TEST_USER);
  await loginForm.locator('input[name="password"]').fill(TEST_PASS);
  await loginForm.locator('button[type="submit"]').click();
  await expect(loginForm).not.toBeVisible({ timeout: 8_000 });
  return true;
}
