/**
 * Signs in once per run and saves the session for every spec to start from.
 *
 * The login route allows 10 attempts per client IP per minute, and a run has
 * more specs than that, so specs that only need to be signed in reuse this
 * session. Specs that test the login form itself start from EMPTY_STATE.
 */
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { request, type FullConfig } from "@playwright/test";

export const STORAGE_STATE = fileURLToPath(new URL("./.auth/state.json", import.meta.url));
export const EMPTY_STATE = { cookies: [], origins: [] };

export default async function globalSetup(config: FullConfig): Promise<void> {
  const baseURL = config.projects[0]?.use.baseURL;
  const ctx = await request.newContext({ baseURL });
  try {
    const me = await ctx.get("/api/v1/me");
    const session = (await me.json().catch(() => ({}))) as { authEnabled?: boolean };
    if (me.ok() && session.authEnabled === false) {
      // No auth on this instance: the specs start signed out and need nothing.
    } else {
      const login = await ctx.post("/api/v1/login", {
        data: {
          username: process.env["BUGBARN_TEST_USER"] ?? "admin",
          password: process.env["BUGBARN_TEST_PASS"] ?? "password",
        },
      });
      if (!login.ok()) throw new Error(`e2e sign-in failed: HTTP ${login.status()}`);
    }
    mkdirSync(dirname(STORAGE_STATE), { recursive: true });
    await ctx.storageState({ path: STORAGE_STATE });
  } finally {
    await ctx.dispose();
  }
}
