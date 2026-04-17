import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

import { captureException, createTransport, flush, getApiKey, init, uploadSourceMap } from "../dist/esm/index.js";

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
  assert.equal(events[0].sender.sdk.version, "0.1.0");
  assert.ok(events[0].timestamp.length > 0);
});

test("flush delegates to transport", async () => {
  let flushed = false;
  const transport = {
    async send() {},
    async flush() {
      flushed = true;
    },
  };

  init({
    apiKey: "bb_live_test",
    transport,
    installDefaultHandlers: false,
  });

  await flush();
  assert.equal(flushed, true);
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
});
