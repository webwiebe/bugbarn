import { defineConfig, devices } from "@playwright/test";

// The post-deploy smoke: one headless Chromium run of e2e/deploy-smoke.spec.ts
// against BUGBARN_SMOKE_URL. The trailing slash matters, the specs navigate
// with relative URLs ("./#/issues") under the dashboard's /app/ prefix.
const baseURL = process.env["BUGBARN_SMOKE_URL"] ?? "http://localhost:4173/app/";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "deploy-smoke.spec.ts",
  timeout: 60_000,
  retries: 0,
  reporter: [["list"]],
  use: { baseURL, ...devices["Desktop Chrome"], trace: "off" },
});
