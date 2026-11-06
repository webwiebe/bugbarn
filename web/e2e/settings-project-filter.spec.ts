/**
 * The project filter on Settings → Projects hides rows and shows an empty state
 * when nothing matches. The projects API is stubbed, so the test needs no seeded data.
 */

import { test, expect } from "./fixtures";
import { loginIfRequired } from "./login-helper";


const PROJECTS = [
  { id: 1, slug: "videoslop", name: "videoslop", status: "active" },
  { id: 2, slug: "bugbarn-service", name: "bugbarn-service", status: "active" },
  { id: 3, slug: "brandtrace", name: "brandtrace", status: "active" },
];

test("the project filter hides rows and shows an empty state", async ({ page }) => {
  await page.route(/\/api\/v1\/projects(\?|$)/, (route) => route.fulfill({ json: { projects: PROJECTS } }));

  await page.goto("./");
  await loginIfRequired(page);
  await page.goto("./#/settings/projects");

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

// Settings renders from the cached project list at once, then renders again when
// loadSettings' fetches land. A query typed in between must survive the second
// render. The ?usage=true response is held until the query is typed, so the
// order is fixed, and the test waits on the real response and on the list the
// second render puts in place.
test("a filter typed before Settings finishes loading survives the re-render", async ({ page }) => {
  let releaseUsage!: () => void;
  const usageHeld = new Promise<void>((resolve) => { releaseUsage = resolve; });
  const isUsage = (url: string): boolean => new URL(url).searchParams.get("usage") === "true";
  await page.route(/\/api\/v1\/projects(\?|$)/, async (route) => {
    if (isUsage(route.request().url())) await usageHeld;
    await route.fulfill({ json: { projects: PROJECTS } });
  });

  await page.goto("./");
  await loginIfRequired(page);
  const usageRequested = page.waitForRequest((req) => req.url().includes("/api/v1/projects") && isUsage(req.url()));
  await page.goto("./#/settings/projects");
  await usageRequested;

  const list = page.locator("#project-list");
  await expect(list.locator(".project-row")).toHaveCount(3, { timeout: 8_000 });
  await page.locator("#project-filter").fill("zzz-nothing");
  await expect(page.locator("#project-empty")).toBeVisible();
  await list.evaluate((el) => { el.dataset["firstRender"] = "1"; });

  const usageAnswered = page.waitForResponse((res) => res.url().includes("/api/v1/projects") && isUsage(res.url()));
  releaseUsage();
  await usageAnswered;
  await expect(page.locator("#project-list[data-first-render]")).toHaveCount(0);
  await expect(list).toHaveCount(1);

  await expect(page.locator("#project-filter")).toHaveValue("zzz-nothing");
  await expect(page.locator("#project-filter")).toBeFocused();
  await expect(page.locator("#project-list .project-row:visible")).toHaveCount(0);
  await expect(page.locator("#project-empty")).toContainText('No projects match "zzz-nothing"');
});
