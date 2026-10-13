import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

import { captureException, checkIn, createTransport, flush, getApiKey, init, shutdown, uploadSourceMap, setUser, clearUser, addBreadcrumb, clearBreadcrumbs } from "../dist/esm/index.js";

test("init stores transport and api key", async () => {
  const events = [];
  const transport = {
    async send(event) {
      events.push(event);
    },
    async flush() {},
  };

  init({
    apiKey: "bb_live_test",
    release: "1.2.3",
    dist: "web",
    transport,
    installDefaultHandlers: false,
  });

  await captureException(new Error("boom"), { tags: { service: "api" } });

  assert.equal(getApiKey(), "bb_live_test");
  assert.equal(events.length, 1);
  assert.equal(events[0].severityText, "ERROR");
  assert.equal(events[0].body, "boom");
  assert.equal(events[0].exception.type, "Error");
  assert.equal(events[0].exception.message, "boom");
  assert.equal(events[0].release, "1.2.3");
  assert.equal(events[0].dist, "web");
  assert.equal(events[0].tags?.service, "api");
  assert.equal(events[0].sender.sdk.name, "bugbarn.typescript");
  assert.equal(events[0].sender.sdk.version, "1.2.0");
  assert.ok(events[0].timestamp.length > 0);
});

test("flush delegates to transport", async () => {
  let flushed = false;
  const transport = {
    async send() {},
    async flush(options) {
      flushed = true;
      assert.equal(options.timeoutMs, 2000);
      return true;
    },
  };

  init({
    apiKey: "bb_live_test",
    transport,
    installDefaultHandlers: false,
  });

  const drained = await flush();
  assert.equal(flushed, true);
  assert.equal(drained, true);
});

test("flush returns false after bounded timeout", async () => {
  const transport = {
    async send() {},
    async flush() {
      await new Promise(() => {});
    },
  };

  init({
    apiKey: "bb_live_test",
    transport,
    installDefaultHandlers: false,
  });

  assert.equal(await flush(5), false);
});

test("shutdown flushes and detaches the transport", async () => {
  let flushes = 0;
  const transport = {
    async send() {
      throw new Error("detached transport should not be used");
    },
    async flush() {
      flushes += 1;
      return true;
    },
  };

  init({
    apiKey: "bb_live_test",
    transport,
    installDefaultHandlers: false,
  });

  assert.equal(await shutdown(25), true);
  await captureException(new Error("after shutdown"));
  assert.equal(flushes, 1);
});

test("transport sends api key header to ingest endpoint", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];

  globalThis.fetch = async (input, init) => {
    calls.push({ url: String(input), init });
    return new Response(null, { status: 200 });
  };

  try {
    const transport = createTransport("bb_live_test", "http://127.0.0.1:9000/api/v1/events");
    await transport.send({
      timestamp: new Date().toISOString(),
      severityText: "ERROR",
      body: "boom",
      exception: { type: "Error", message: "boom" },
      sender: {
        sdk: {
          name: "bugbarn.typescript",
          version: "0.1.0",
        },
      },
    });
    await transport.flush();
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "http://127.0.0.1:9000/api/v1/events");
  assert.equal(new Headers(calls[0].init?.headers).get("x-bugbarn-api-key"), "bb_live_test");
  const body = JSON.parse(String(calls[0].init?.body));
  assert.equal(body.body, "boom");
  assert.equal(body.exception.message, "boom");
  assert.equal(body.sender.sdk.name, "bugbarn.typescript");
});

test("uploadSourceMap posts release metadata and artifact contents", async () => {
  const originalFetch = globalThis.fetch;
  const calls = [];

  globalThis.fetch = async (input, init) => {
    calls.push({ url: String(input), init });
    return new Response(null, { status: 202 });
  };

  try {
    await uploadSourceMap({
      apiKey: "bb_live_test",
      endpoint: "http://127.0.0.1:9000/api/v1/source-maps",
      release: "1.2.3",
      dist: "web",
      bundleUrl: "https://example.test/assets/app.js",
      sourceMap: "{\"version\":3,\"file\":\"app.js\"}",
      sourceMapFilename: "app.js.map",
    });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "http://127.0.0.1:9000/api/v1/source-maps");
  assert.equal(new Headers(calls[0].init?.headers).get("x-bugbarn-api-key"), "bb_live_test");

  const formData = calls[0].init?.body;
  assert.ok(formData instanceof FormData);
  assert.equal(formData.get("release"), "1.2.3");
  assert.equal(formData.get("dist"), "web");
  assert.equal(formData.get("bundle_url"), "https://example.test/assets/app.js");

  const sourceMap = formData.get("source_map");
  assert.ok(sourceMap instanceof Blob);
  assert.equal(await sourceMap.text(), "{\"version\":3,\"file\":\"app.js\"}");
});

test("commonjs consumers can require the package entry", async () => {
  const require = createRequire(import.meta.url);
  const sdk = require("../dist/cjs/index.js");
  assert.equal(typeof sdk.init, "function");
  assert.equal(typeof sdk.captureException, "function");
  assert.equal(typeof sdk.createTransport, "function");
  assert.equal(typeof sdk.shutdown, "function");
});

test("buildEnvelope includes user context when set", async () => {
  clearUser();
  clearBreadcrumbs();
  const events = [];
  const transport = {
    async send(event) { events.push(event); },
    async flush() {},
  };

  init({ apiKey: "bb_live_test", transport, installDefaultHandlers: false, autoBreadcrumbs: false });

  setUser({ id: "u1", email: "user@example.com", username: "tester" });
  await captureException(new Error("with-user"));

  assert.equal(events.length >= 1, true);
  const event = events[events.length - 1];
  assert.deepEqual(event.user, { id: "u1", email: "user@example.com", username: "tester" });

  clearUser();
});

test("buildEnvelope omits user when not set", async () => {
  clearUser();
  clearBreadcrumbs();
  const events = [];
  const transport = {
    async send(event) { events.push(event); },
    async flush() {},
  };

  init({ apiKey: "bb_live_test", transport, installDefaultHandlers: false, autoBreadcrumbs: false });
  await captureException(new Error("no-user"));

  const event = events[events.length - 1];
  assert.equal(event.user, undefined);
});

test("buildEnvelope includes breadcrumbs when present", async () => {
  clearUser();
  clearBreadcrumbs();
  const events = [];
  const transport = {
    async send(event) { events.push(event); },
    async flush() {},
  };

  init({ apiKey: "bb_live_test", transport, installDefaultHandlers: false, autoBreadcrumbs: false });

  addBreadcrumb({ timestamp: new Date().toISOString(), category: "manual", message: "step one" });
  addBreadcrumb({ timestamp: new Date().toISOString(), category: "manual", message: "step two" });
  await captureException(new Error("with-crumbs"));

  const event = events[events.length - 1];
  assert.ok(Array.isArray(event.breadcrumbs));
  assert.ok(event.breadcrumbs.length >= 2);
  assert.ok(event.breadcrumbs.some((c) => c.message === "step one"));
  assert.ok(event.breadcrumbs.some((c) => c.message === "step two"));

  clearBreadcrumbs();
});

test("buildEnvelope omits breadcrumbs when buffer is empty", async () => {
  clearUser();
  clearBreadcrumbs();
  const events = [];
  const transport = {
    async send(event) { events.push(event); },
    async flush() {},
  };

  init({ apiKey: "bb_live_test", transport, installDefaultHandlers: false, autoBreadcrumbs: false });
  await captureException(new Error("no-crumbs"));

  const event = events[events.length - 1];
  assert.equal(event.breadcrumbs, undefined);
});

test("environment option is sent as the environment attribute", async () => {
  const events = [];
  const transport = { async send(event) { events.push(event); }, async flush() {} };

  init({ apiKey: "k", environment: "staging", transport, installDefaultHandlers: false });
  await captureException(new Error("a"));
  await captureException(new Error("b"), { attributes: { region: "eu" } });
  await captureException(new Error("c"), { attributes: { environment: "override" } });

  assert.deepEqual(events[0].attributes, { environment: "staging", "deployment.environment": "staging" });
  assert.deepEqual(events[1].attributes, { region: "eu", environment: "staging", "deployment.environment": "staging" });
  assert.equal(events[2].attributes.environment, "override");
  assert.equal(events[2].attributes["deployment.environment"], "override");
});

test("environment falls back to BUGBARN_ENVIRONMENT and is omitted when unset", async () => {
  const events = [];
  const transport = { async send(event) { events.push(event); }, async flush() {} };

  process.env.BUGBARN_ENVIRONMENT = "testing";
  init({ apiKey: "k", transport, installDefaultHandlers: false });
  await captureException(new Error("a"));
  delete process.env.BUGBARN_ENVIRONMENT;
  init({ apiKey: "k", transport, installDefaultHandlers: false });
  await captureException(new Error("b"));

  assert.equal(events[0].attributes.environment, "testing");
  assert.equal(events[1].attributes, undefined);
});

test("checkIn builds the body from seconds and duration strings", async () => {
  const sent = [];
  const transport = {
    async send() {},
    async sendCheckIn(slug, body) { sent.push({ slug, body }); },
    async flush() {},
  };
  init({ apiKey: "k", transport, installDefaultHandlers: false });

  assert.equal(await checkIn("nightly", "error", { interval: 3600, grace: "10m", message: "disk full" }), true);
  assert.equal(await checkIn("nightly", "ok", { interval: "1h" }), true);
  assert.equal(await checkIn("nightly"), true);

  assert.deepEqual(sent[0], { slug: "nightly", body: { status: "error", interval: "3600s", grace: "10m", message: "disk full" } });
  assert.deepEqual(sent[1].body, { status: "ok", interval: "1h" });
  assert.deepEqual(sent[2].body, { status: "ok" });
});

test("checkIn sends a cron schedule with its time zone", async () => {
  const sent = [];
  const transport = {
    async send() {},
    async sendCheckIn(slug, body) { sent.push({ slug, body }); },
    async flush() {},
  };
  init({ apiKey: "k", transport, installDefaultHandlers: false });

  assert.equal(await checkIn("nightly", "ok", { schedule: "0 6 * * *", timezone: "Europe/Amsterdam", grace: "10m" }), true);
  assert.equal(await checkIn("nightly", "ok", { schedule: "0 6 * * *" }), true);
  assert.deepEqual(sent[0].body, { status: "ok", grace: "10m", schedule: "0 6 * * *", timezone: "Europe/Amsterdam" });
  assert.deepEqual(sent[1].body, { status: "ok", schedule: "0 6 * * *" });
  await assert.rejects(() => checkIn("nightly", "ok", { interval: 3600, schedule: "0 6 * * *" }), RangeError);
  await assert.rejects(() => checkIn("nightly", "ok", { timezone: "Europe/Amsterdam" }), RangeError);
});

test("checkIn rejects bad input and reports transports that cannot carry it", async () => {
  init({ apiKey: "k", transport: { async send() {}, async flush() {} }, installDefaultHandlers: false });
  assert.equal(await checkIn("nightly"), false);
  await assert.rejects(() => checkIn("nightly", "fine"), RangeError);
  await assert.rejects(() => checkIn("nightly", "ok", { interval: 0 }), RangeError);

  await shutdown();
  assert.equal(await checkIn("nightly"), false);
});

test("default transport posts check-ins next to the events endpoint and flush waits for them", async () => {
  const calls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    await new Promise((resolve) => setTimeout(resolve, 20));
    calls.push({ url, method: init.method, headers: init.headers, body: JSON.parse(init.body) });
    return { ok: true, status: 200 };
  };
  try {
    const transport = createTransport("bb_live_test", "https://bugbarn.example.com/api/v1/events", "geo");
    await transport.sendCheckIn("pending sweep", { status: "ok", interval: "1h" });
    assert.equal(calls.length, 0);
    assert.equal(await transport.flush({ timeoutMs: 2000 }), true);

    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "https://bugbarn.example.com/api/v1/monitors/pending%20sweep/check-ins");
    assert.equal(calls[0].method, "POST");
    assert.equal(calls[0].headers["x-bugbarn-api-key"], "bb_live_test");
    assert.equal(calls[0].headers["x-bugbarn-project"], "geo");
    assert.deepEqual(calls[0].body, { status: "ok", interval: "1h" });
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("a failing check-in request never throws into the caller", async () => {
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () => { throw new Error("network down"); };
  try {
    const transport = createTransport("k", "https://bugbarn.example.com");
    await transport.sendCheckIn("nightly", { status: "ok" });
    assert.equal(await transport.flush({ timeoutMs: 2000 }), true);
  } finally {
    globalThis.fetch = realFetch;
  }
});
