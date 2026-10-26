/**
 * Post-deploy browser smoke. Loads the deployed dashboard in headless Chromium,
 * signs in, opens the main views and fails on any console error, uncaught page
 * error, failed request or 5xx response, and on a view that does not render.
 *
 * It runs after the rollout of every testing deploy (.woodpecker/build-and-test.yaml)
 * and locally against any build (`pnpm run smoke:deploy`, see playwright.smoke.config.ts).
 *
 * Environment:
 *   BUGBARN_SMOKE_URL       dashboard base URL including the path, e.g. https://host/app/
 *   BUGBARN_SMOKE_USER      admin username
 *   BUGBARN_SMOKE_PASSWORD  admin password
 *   BUGBARN_SMOKE_API_KEY   ingest key, used to seed one issue so the issue page can be opened
 */

import { test, expect, type Page } from "@playwright/test";

const USER = process.env["BUGBARN_SMOKE_USER"] ?? "";
const PASSWORD = process.env["BUGBARN_SMOKE_PASSWORD"] ?? "";
const API_KEY = process.env["BUGBARN_SMOKE_API_KEY"] ?? "";

/**
 * 4xx answers the app produces on a healthy deploy: the session probe answers 401
 * until sign-in, and the pre-login telemetry post answers 403 (no CSRF token yet).
 */
function isExpectedClientError(status: number, url: string): boolean {
  const path = new URL(url).pathname;
  return (status === 401 && path.endsWith("/api/v1/me")) || (status === 403 && path.endsWith("/api/v1/telemetry"));
}

/** Collects everything that means "the page is broken" while a test runs. */
function watchForErrors(page: Page): string[] {
  const problems: string[] = [];
  page.on("console", (msg) => {
    // The browser logs every 4xx/5xx subresource as "Failed to load resource"
    // without a URL; the response handler below judges those by URL instead.
    if (msg.type() === "error" && !msg.text().startsWith("Failed to load resource")) problems.push(`console.error: ${msg.text()}`);
  });
  page.on("pageerror", (err) => problems.push(`pageerror: ${err.message}`));
  page.on("requestfailed", (req) => {
    const reason = req.failure()?.errorText ?? "";
    // A navigation away or an EventSource closing on teardown aborts requests.
    if (!reason.includes("ABORTED")) problems.push(`requestfailed: ${req.method()} ${req.url()} ${reason}`);
  });
  page.on("response", (res) => {
    const status = res.status();
    if (status < 400 || isExpectedClientError(status, res.url())) return;
    problems.push(`HTTP ${status}: ${res.request().method()} ${res.url()}`);
  });
  return problems;
}

async function signIn(page: Page): Promise<void> {
  const form = page.locator("#login-form");
  await expect(form).toBeVisible({ timeout: 15_000 });
  await form.locator('input[name="username"]').fill(USER);
  await form.locator('input[name="password"]').fill(PASSWORD);
  await form.locator('button[type="submit"]').click();
  await expect(form).not.toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#issue-list")).toBeVisible({ timeout: 15_000 });
}

/** After a reload the session cookie keeps the user in; sign in again only if the form shows. */
async function signInIfAsked(page: Page): Promise<void> {
  await page.locator("#login-form:visible, #issue-list:visible").first().waitFor({ timeout: 15_000 });
  if (await page.locator("#login-form").isVisible()) await signIn(page);
}

/** Posts one event so the issue list is never empty, and returns the title it will show. */
async function seedIssue(page: Page): Promise<string> {
  const stamp = new Date().toISOString();
  const response = await page.request.post("/api/v1/events", {
    headers: { "X-BugBarn-Api-Key": API_KEY, "X-BugBarn-Project": "deploy-smoke" },
    data: {
      timestamp: stamp,
      severityText: "error",
      body: "deploy smoke event",
      exception: { type: "DeploySmokeError", message: "deploy smoke event", stacktrace: [{ function: "smoke", filename: "smoke.js", lineno: 1 }] },
      attributes: { environment: "smoke" },
    },
  });
  expect(response.status(), "seed event accepted").toBeLessThan(300);
  return "DeploySmokeError";
}

test.beforeAll(() => {
  for (const [name, value] of Object.entries({ BUGBARN_SMOKE_USER: USER, BUGBARN_SMOKE_PASSWORD: PASSWORD, BUGBARN_SMOKE_API_KEY: API_KEY })) {
    if (!value) throw new Error(`${name} is not set`);
  }
});

test("signed out, the app redirects to the login form", async ({ page }) => {
  const problems = watchForErrors(page);
  await page.goto("./#/issues");
  await expect(page.locator("#login-form")).toBeVisible({ timeout: 15_000 });
  // The login screen replaces the app: no issue rows can be visible.
  await expect(page.locator("#issue-list .issue-row")).toHaveCount(0);
  // The 401 from the session probe is the redirect itself (see isExpectedClientError).
  expect(problems).toEqual([]);
});

test("signed in, the projects list, issue list, issue page and other views render", async ({ page }) => {
  const problems = watchForErrors(page);
  // Seed before the page loads: the worker turns the event into an issue and a
  // project asynchronously, and the app reads both lists when it starts.
  await seedIssue(page);
  await page.goto("./");
  await signIn(page);

  // Issue list and issue page. The event is processed asynchronously, so reload
  // until its row shows up.
  const row = page.locator("#issue-list .issue-row", { hasText: "DeploySmokeError" }).first();
  await expect(async () => {
    await page.reload();
    await signInIfAsked(page);
    await expect(row).toBeVisible({ timeout: 3_000 });
  }).toPass({ timeout: 45_000 });

  // Projects list: the picker opens and lists the seeded project.
  await page.locator("#project-picker-btn").click();
  await expect(page.locator("#project-picker-dropdown")).toBeVisible();
  await expect(page.locator("#project-picker-list")).toContainText("deploy-smoke", { timeout: 15_000 });
  await page.locator("#project-picker-btn").click();

  await row.click();
  await expect(page.locator("#detail-view .issue-hero h3")).not.toBeEmpty({ timeout: 15_000 });

  // The other top-level views each render their heading and no error card.
  for (const [hash, title] of [
    ["releases", "Releases"],
    ["alerts", "Alerts"],
    ["logs", "Logs"],
    ["rules", "Detection rules"],
    ["monitors", "Monitors"],
    ["settings", "Settings"],
  ] as const) {
    await page.goto(`./#/${hash}`);
    await expect(page.locator("#topbar-title"), `${hash} view heading`).toHaveText(title, { timeout: 15_000 });
    await expect(page.locator("#detail-body .error, #overview-view .error"), `${hash} view error card`).toHaveCount(0);
  }

  expect(problems).toEqual([]);
});
