/**
 * The project filter on Settings → Projects hides rows and shows an empty state
 * when nothing matches. The projects API is stubbed, so the test needs no seeded data.
 */

import { test, expect } from "@playwright/test";
import { loginIfRequired } from "./login-helper";


const PROJECTS = [
  { id: 1, slug: "videoslop", name: "videoslop", status: "active" },
  { id: 2, slug: "bugbarn-service", name: "bugbarn-service", status: "active" },
  { id: 3, slug: "brandtrace", name: "brandtrace", status: "active" },
];

test("the project filter hides rows and shows an empty state", async ({ page }) => {
  await page.route(/\/api\/v1\/projects(\?|$)/, (route) => route.fulfill({ json: { projects: PROJECTS } }));

  await page.goto("/");
  await loginIfRequired(page);
  await page.goto("/#/settings/projects");

  const rows = page.locator("#project-list .project-row");
  await expect(rows).toHaveCount(3, { timeout: 8_000 });
  await expect(rows.locator("visible=true")).toHaveCount(3);

  await page.locator("#project-filter").fill("videoslop");
  await expect(page.locator("#project-list .project-row:visible")).toHaveCount(1);

  await page.locator("#project-filter").fill("zzz-nothing");
  await expect(page.locator("#project-list .project-row:visible")).toHaveCount(0);
  await expect(page.locator("#project-empty")).toBeVisible();
  await expect(page.locator("#project-empty")).toContainText('No projects match "zzz-nothing"');

  await page.locator("#project-empty-clear").click();
  await expect(page.locator("#project-list .project-row:visible")).toHaveCount(3);
  await expect(page.locator("#project-empty")).toBeHidden();
});
