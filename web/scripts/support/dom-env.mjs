// Test environment for the SPA modules that touch the DOM at import time.
// Importing this file registers the ".js" to ".ts" resolver, builds a jsdom
// window from the real index.html, and installs the browser globals the
// sources read. Import it before any module under src/.
import { register } from "node:module";
import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";

register("./ts-resolve-hooks.mjs", import.meta.url);

const html = readFileSync(new URL("../../index.html", import.meta.url), "utf8").replace(
  /<script[\s\S]*?<\/script>/g,
  "",
);

const dom = new JSDOM(html, { url: "https://bugbarn.test/", pretendToBeVisual: true });
export const win = dom.window;

const names = [
  "document", "localStorage", "sessionStorage", "location", "history", "HTMLElement",
  "Element", "Node", "Event", "CustomEvent", "KeyboardEvent", "MouseEvent", "FormData",
  "DOMParser", "getComputedStyle",
];
for (const name of names) {
  Object.defineProperty(globalThis, name, { value: win[name], configurable: true, writable: true });
}
Object.defineProperty(globalThis, "CSS", { value: { escape: (v) => String(v).replace(/[^\w-]/g, (c) => `\\${c}`) }, configurable: true });

/** URLs the app navigated to through window.location.assign (jsdom cannot navigate). */
export const navigations = [];
const bound = (target, prop) => {
  const value = target[prop];
  return typeof value === "function" ? value.bind(target) : value;
};
// The proxies wrap an empty object: jsdom's own properties are non-configurable,
// and a proxy may only differ from its target on configurable ones.
const locationProxy = new Proxy({}, {
  get: (_, prop) => (prop === "assign" ? (url) => navigations.push(String(url)) : bound(win.location, prop)),
  set: (_, prop, value) => Reflect.set(win.location, prop, value),
});
const windowProxy = new Proxy({}, {
  get: (_, prop) => (prop === "location" ? locationProxy : bound(win, prop)),
  set: (_, prop, value) => Reflect.set(win, prop, value),
  has: (_, prop) => prop in win,
});
Object.defineProperty(globalThis, "window", { value: windowProxy, configurable: true, writable: true });
Object.defineProperty(globalThis, "navigator", { value: win.navigator, configurable: true });

/** Fetch stub. Routes are keyed "METHOD /path-prefix" (GET when no method); unmatched calls throw. */
export function stubFetch(routes) {
  const calls = [];
  const fn = async (input, init = {}) => {
    const url = typeof input === "string" ? input : input.url;
    const method = (init.method || "GET").toUpperCase();
    calls.push({ url, method, init });
    for (const [key, handler] of Object.entries(routes)) {
      const [m, prefix] = key.includes(" ") ? key.split(" ") : ["GET", key];
      if (m === method && url.startsWith(prefix)) {
        const out = typeof handler === "function" ? handler({ url, method, init }) : handler;
        const status = out?.status ?? 200;
        const body = out && typeof out === "object" && "body" in out ? out.body : out;
        const text = typeof body === "string" ? body : JSON.stringify(body ?? {});
        return new Response(text, { status, headers: { "Content-Type": "application/json" } });
      }
    }
    // A write nobody routed succeeds with an empty body; an unrouted read is a test bug.
    if (method !== "GET") return new Response("{}", { status: 200 });
    throw new Error(`unrouted fetch ${method} ${url}`);
  };
  fn.calls = calls;
  globalThis.fetch = fn;
  win.fetch = fn;
  return fn;
}

/** Waits for queued microtasks and zero-delay timers. */
export const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
