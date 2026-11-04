import { defineConfig, devices } from "@playwright/test";
import { config } from "dotenv";
import { STORAGE_STATE } from "./e2e/global-setup";

config();

const baseURL = process.env["BUGBARN_TEST_URL"] ?? "http://localhost:8080";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  globalSetup: "./e2e/global-setup.ts",
  use: {
    baseURL,
    storageState: STORAGE_STATE,
  },
  projects: [
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "tablet",
      use: {
        viewport: { width: 1024, height: 768 },
        defaultBrowserType: "chromium",
      },
    },
    {
      name: "mobile",
      use: {
        ...devices["iPhone 14"],
        defaultBrowserType: "chromium",
      },
    },
  ],
});
