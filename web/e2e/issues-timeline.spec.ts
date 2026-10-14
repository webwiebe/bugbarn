/**
 * The issues page shows errors per project as a stacked chart with one legend
 * entry per project. The timeline and issues APIs are stubbed, so the test
 * needs no seeded data.
 */

import { test, expect, type Route } from "@playwright/test";
import { loginIfRequired } from "./login-helper";


function timeline(range: string): Record<string, unknown> {
  return {
    range,
    bucket_seconds: 3600,
    buckets: [
      { start: "2026-10-14T08:00:00Z", counts: {} },
      { start: "2026-10-14T09:00:00Z", counts: { web: 4, api: 1 } },
      { start: "2026-10-14T10:00:00Z", counts: { web: 2, worker: 3 } },
    ],
    projects: [
      { slug: "web", name: "Web", total: 6 },
      { slug: "worker", name: "Worker", total: 3 },
      { slug: "api", name: "API", total: 1 },
    ],
  };
}

test("the issues page shows the error chart with a legend entry per project", async ({ page }) => {
  const ranges: string[] = [];
  await page.route(/\/api\/v1\/events\/timeline/, async (route: Route) => {
    const range = new URL(route.request().url()).searchParams.get("range") ?? "";
    ranges.push(range);
    await route.fulfill({ json: timeline(range) });
  });
  await page.route(/\/api\/v1\/issues(\?|$)/, (route) => route.fulfill({ json: { issues: [], hasMore: false, total: 0 } }));
  await page.route("**/api/v1/issues/sparklines*", (route) => route.fulfill({ json: { sparklines: {} } }));

  await page.goto("/");
  await loginIfRequired(page);
  await page.goto("/#/issues");

  const legend = page.locator("#error-timeline .error-timeline-legend-item");
  await expect(legend).toHaveCount(3, { timeout: 8_000 });
  for (const name of ["Web", "Worker", "API"]) {
    await expect(legend.filter({ hasText: name })).toHaveCount(1);
  }
  await expect(page.locator("#error-timeline .error-timeline-bar")).toHaveCount(3);
  expect(ranges[0]).toBe("24h");

  await page.locator('[data-timeline-range="7d"]').click();
  await expect.poll(() => ranges.includes("7d")).toBe(true);
});
