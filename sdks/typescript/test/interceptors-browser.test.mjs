// Runs in its own process so the browser globals faked here cannot leak into
// other test files.
import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";

class FakeXHR {
  constructor() {
    this.listeners = {};
    this.status = 0;
    this.opened = null;
  }
  open(method, url) {
    this.opened = [method, url];
  }
  send() {
    this.sent = true;
  }
  addEventListener(name, fn) {
    this.listeners[name] = fn;
  }
}

const windowListeners = {};
const location = { href: "https://app.test/" };
const historyCalls = [];
globalThis.XMLHttpRequest = FakeXHR;
globalThis.window = {
  location,
  addEventListener(name, fn) {
    windowListeners[name] = fn;
  },
};
globalThis.history = {
  pushState: (...args) => historyCalls.push(["push", ...args]),
  replaceState: (...args) => historyCalls.push(["replace", ...args]),
};

const { getBreadcrumbs, clearBreadcrumbs } = await import("../dist/esm/breadcrumbs.js");
const { installAutoInterceptors } = await import("../dist/esm/interceptors.js");

const server = http.createServer((_req, res) => {
  res.statusCode = 204;
  res.end();
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;
test.after(() => new Promise((resolve) => server.close(resolve)));

installAutoInterceptors();

test("fetch interceptor records method, url and status for string, URL and Request inputs", async () => {
  clearBreadcrumbs();
  await fetch(`${base}/a`);
  await fetch(new URL(`${base}/b`), { method: "post" });
  await fetch(new Request(`${base}/c`));

  const crumbs = getBreadcrumbs().filter((c) => c.category === "http");
  assert.deepEqual(
    crumbs.map((c) => c.message),
    [`GET ${base}/a`, `POST ${base}/b`, `GET ${base}/c`],
  );
  assert.equal(crumbs[0].data.status_code, 204);
  assert.equal(crumbs[1].data.method, "POST");
});

test("XHR interceptor records a breadcrumb on loadend", () => {
  clearBreadcrumbs();
  const xhr = new FakeXHR();
  xhr.open("PUT", "/upload");
  assert.deepEqual(xhr.opened, ["PUT", "/upload"]);
  xhr.send("body");
  assert.equal(xhr.sent, true);
  xhr.status = 201;
  xhr.listeners.loadend();

  const crumb = getBreadcrumbs().find((c) => c.category === "http");
  assert.equal(crumb.message, "PUT /upload");
  assert.deepEqual(crumb.data, { method: "PUT", url: "/upload", status_code: 201 });
});

test("XHR interceptor falls back to placeholders when open was never called", () => {
  clearBreadcrumbs();
  const xhr = new FakeXHR();
  xhr.send();
  xhr.listeners.loadend();
  const crumb = getBreadcrumbs().find((c) => c.category === "http");
  assert.equal(crumb.message, "XHR ");
});

test("navigation interceptor records hashchange, pushState and replaceState", () => {
  clearBreadcrumbs();
  location.href = "https://app.test/#one";
  windowListeners.hashchange();
  history.pushState({}, "", "/two");
  location.href = "https://app.test/two";
  history.pushState({}, "", "/two");
  location.href = "https://app.test/three";
  history.replaceState({}, "", "/three");

  const nav = getBreadcrumbs().filter((c) => c.category === "navigation");
  assert.equal(nav.length, 4);
  assert.deepEqual(nav[0].data, { from: "https://app.test/", to: "https://app.test/#one" });
  assert.equal(nav[0].message, "Navigated to https://app.test/#one");
  assert.deepEqual(nav[1].data, { from: "https://app.test/#one", to: "https://app.test/#one" });
  assert.equal(nav[3].data.to, "https://app.test/three");
  assert.equal(historyCalls.length, 3);
});
