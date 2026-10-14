/**
 * The issues list loads the next page on scroll. The issues API is stubbed with
 * two pages so the test needs no seeded data; the server only has to serve the
 * app and, when auth is on, accept the test login.
 */

import { test, expect, type Route } from "@playwright/test";
import { loginIfRequired } from "./login-helper";

const PAGE_SIZE = 50;
const TOTAL = 80;

function issuePage(offset: number): Record<string, unknown> {
  const count = Math.min(PAGE_SIZE, TOTAL - offset);
  const issues = Array.from({ length: count }, (_, i) => {
    const n = offset + i + 1;
    return {
      id: `scroll-${n}`,
      title: `Scroll test issue ${n}`,
      status: "open",
      event_count: 1,
      first_seen: "2026-10-14T00:00:00Z",
      last_seen: "2026-10-14T00:00:00Z",
    };
  });
  return { issues, hasMore: offset + count < TOTAL, total: TOTAL };
}

test("scrolling to the end of the issues list loads the next page", async ({ page }) => {
  const offsets: number[] = [];
  await page.route(/\/api\/v1\/issues(\?|$)/, async (route: Route) => {
    const url = new URL(route.request().url());
    const offset = Number(url.searchParams.get("offset") ?? "0");
    offsets.push(offset);
    await route.fulfill({ json: issuePage(offset) });
  });
  await page.route("**/api/v1/issues/sparklines*", (route) => route.fulfill({ json: { sparklines: {} } }));

  await page.goto("/");
  await loginIfRequired(page);
  await page.goto("/#/issues");

  const rows = page.locator("#issue-list .issue-row");
  await expect(rows).toHaveCount(PAGE_SIZE, { timeout: 8_000 });
  expect(offsets.filter((o) => o > 0)).toEqual([]);

  // The list re-renders while sparklines and totals settle, which replaces the
  // sentinel node, so scroll the page itself instead of the node.
  await page.mouse.move(400, 400);
  await page.mouse.wheel(0, 100_000);

  await expect(rows).toHaveCount(TOTAL, { timeout: 8_000 });
  expect(offsets.filter((o) => o > 0)).toEqual([PAGE_SIZE]);
  await expect(page.locator("#issues-end")).toBeVisible();
  await expect(page.locator("#load-more-issues")).toHaveCount(0);
});
