// Unit test for the app-level action hooks that keep http and the project views from
// importing the router. Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { loadProjects, refreshAll, registerAppActions, setRouteStatus } from "../src/app-actions.ts";

test("before registration every action does nothing", async () => {
  await refreshAll();
  await loadProjects();
  setRouteStatus();
});

test("registered actions are the ones that run", async () => {
  const calls = [];
  registerAppActions({
    refreshAll: async () => { calls.push("refreshAll"); },
    loadProjects: async () => { calls.push("loadProjects"); },
    setRouteStatus: () => { calls.push("setRouteStatus"); },
  });
  await Promise.all([refreshAll(), loadProjects()]);
  setRouteStatus();
  assert.deepEqual(calls.sort(), ["loadProjects", "refreshAll", "setRouteStatus"]);
});
