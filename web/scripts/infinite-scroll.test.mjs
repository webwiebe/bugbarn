// Unit test for the infinite-scroll controller, with a mocked IntersectionObserver.
// Node strips the types when it loads the .ts source directly.

import test from "node:test";
import assert from "node:assert/strict";
import { createPageLoader, observeSentinel, SCROLL_ROOT_MARGIN } from "../src/infinite-scroll.ts";

class FakeObserver {
  static last = null;
  constructor(callback, options) {
    this.callback = callback;
    this.options = options;
    this.observed = [];
    FakeObserver.last = this;
  }
  observe(el) {
    this.observed.push(el);
  }
  disconnect() {
    this.observed = [];
  }
  fire(isIntersecting) {
    this.callback([{ isIntersecting }]);
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function setup({ canLoad = () => true } = {}) {
  const calls = { count: 0 };
  const gate = { current: deferred() };
  const loader = createPageLoader(async () => {
    calls.count += 1;
    await gate.current.promise;
  });
  const sentinel = {};
  const observer = observeSentinel({ sentinel, canLoad, loader, observerCtor: FakeObserver });
  return { calls, gate, loader, sentinel, observer };
}

test("observes the sentinel with a 400px margin", () => {
  const { sentinel, observer } = setup();
  assert.equal(SCROLL_ROOT_MARGIN, "400px");
  assert.equal(observer.options.rootMargin, "400px");
  assert.deepEqual(observer.observed, [sentinel]);
});

test("loads one page when the sentinel becomes visible", async () => {
  const { calls, gate, observer } = setup();
  observer.fire(true);
  assert.equal(calls.count, 1);
  gate.current.resolve();
  await new Promise((r) => setImmediate(r));
});

test("does not load while the sentinel is out of view", () => {
  const { calls, observer } = setup();
  observer.fire(false);
  assert.equal(calls.count, 0);
});

test("starts no second load while one is in flight", async () => {
  const { calls, gate, loader, observer } = setup();
  observer.fire(true);
  observer.fire(true);
  observer.fire(true);
  assert.equal(calls.count, 1);
  assert.equal(loader.inFlight(), true);
  gate.current.resolve();
  await new Promise((r) => setImmediate(r));
  assert.equal(loader.inFlight(), false);
  gate.current = deferred();
  observer.fire(true);
  assert.equal(calls.count, 2, "a new load may start once the first one finished");
});

test("does not load when there is nothing more to load", () => {
  const { calls, observer } = setup({ canLoad: () => false });
  observer.fire(true);
  assert.equal(calls.count, 0);
});

test("a failing load releases the in-flight guard", async () => {
  let attempts = 0;
  const loader = createPageLoader(async () => {
    attempts += 1;
    throw new Error("boom");
  });
  await assert.rejects(loader.run());
  assert.equal(loader.inFlight(), false);
  await assert.rejects(loader.run());
  assert.equal(attempts, 2);
});

test("returns null without IntersectionObserver so the button stays", () => {
  assert.equal(typeof globalThis.IntersectionObserver, "undefined");
  const loader = createPageLoader(async () => {});
  assert.equal(observeSentinel({ sentinel: {}, canLoad: () => true, loader }), null);
});
