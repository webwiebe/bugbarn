/**
 * Frontend tracing: the SPA starts the SpanBarn browser SDK only when
 * /api/v1/runtime-config advertises the relay, and sends spans to the relay
 * route on its own origin. The runtime config and the relay are stubbed, so
 * the tests need only a host that serves the built SPA.
 */

import { test, expect, type Page } from "@playwright/test";

const RELAY_SPANS = "**/api/v1/frontend-traces/api/v1/spans";

interface Captured {
  spans: Array<Record<string, unknown>>;
  posts: number;
}

async function stubRuntimeConfig(page: Page, spanbarn: Record<string, unknown>): Promise<void> {
  await page.route("**/api/v1/runtime-config", (route) => route.fulfill({ json: { spanbarn } }));
}

async function captureRelay(page: Page): Promise<Captured> {
  const captured: Captured = { spans: [], posts: 0 };
  await page.route(RELAY_SPANS, async (route) => {
    captured.posts += 1;
    const body = route.request().postDataJSON() as { spans?: Array<Record<string, unknown>> } | null;
    captured.spans.push(...(body?.spans ?? []));
    await route.fulfill({ status: 202, body: "" });
  });
  return captured;
}

test("starts the SpanBarn SDK and relays spans when the runtime config enables it", async ({ page }) => {
  await stubRuntimeConfig(page, {
    enabled: true,
    endpoint: "/api/v1/frontend-traces",
    service: "bugbarn-web",
  });
  const captured = await captureRelay(page);

  await page.goto("/");
  // Leaving the page flushes the SDK queue.
  await expect.poll(async () => {
    await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
    return captured.spans.length;
  }, { timeout: 15_000 }).toBeGreaterThan(0);

  const services = new Set(captured.spans.map((s) => s["service"]));
  expect([...services]).toEqual(["bugbarn-web"]);
});

test("sends traceparent on API calls when the SDK is on", async ({ page }) => {
  await stubRuntimeConfig(page, {
    enabled: true,
    endpoint: "/api/v1/frontend-traces",
    service: "bugbarn-web",
  });
  await captureRelay(page);

  const traceparents: string[] = [];
  page.on("request", (req) => {
    const header = req.headers()["traceparent"];
    if (header && new URL(req.url()).pathname.startsWith("/api/v1/") && !req.url().includes("frontend-traces")) {
      traceparents.push(header);
    }
  });

  await page.goto("/");
  await expect.poll(() => traceparents.length, { timeout: 15_000 }).toBeGreaterThan(0);
  expect(traceparents[0]).toMatch(/^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/);
});

test("keeps the relay off when the runtime config leaves it disabled", async ({ page }) => {
  await stubRuntimeConfig(page, { enabled: false });
  const captured = await captureRelay(page);

  await page.goto("/");
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await page.waitForTimeout(500);

  expect(captured.posts).toBe(0);
});
