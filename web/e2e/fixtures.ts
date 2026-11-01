/**
 * The test fixture for specs that sign in on the login form.
 *
 * An environment with OIDC on sends a signed-out page to its identity provider
 * (maybeRedirectToOIDC in src/http.ts), so the form never stays on screen. These
 * specs use a local login on that form, so the page sees the runtime config with
 * OIDC turned off and the rest of it unchanged. Requests that would lead to the
 * identity provider are aborted, so test users do not reach it. The login route
 * is aborted too, because page.route does not see the redirect that follows it.
 *
 * Service workers are blocked because page.route does not see their requests.
 */
import { test as base, expect } from "@playwright/test";

export const test = base.extend({
  serviceWorkers: "block",
  page: async ({ page }, use) => {
    await page.route("**/api/v1/runtime-config", async (route) => {
      const response = await route.fetch();
      const config = await response.json();
      await route.fulfill({ response, json: { ...config, oidc: { ...config.oidc, enabled: false } } });
    });
    await page.route(/\/api\/v1\/oidc\/|^https:\/\/iam\./, (route) => route.abort());
    await use(page);
  },
});

export { expect };
