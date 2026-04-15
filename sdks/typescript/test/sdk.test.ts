import assert from "node:assert/strict";
import test from "node:test";

import { captureException, createTransport, flush, getApiKey, init } from "../src/index.ts";
import type { BugBarnEvent, Transport } from "../src/types.ts";

test("init stores transport and api key", async () => {
  const events: BugBarnEvent[] = [];
  const transport: Transport = {
    async send(event) {
      events.push(event);
    },
    async flush() {},
  };

  init({
    apiKey: "bb_live_test",
    transport,
    installDefaultHandlers: false,
  });

  await captureException(new Error("boom"), { tags: { service: "api" } });

  assert.equal(getApiKey(), "bb_live_test");
  assert.equal(events.length, 1);
  assert.equal(events[0].sdk, "bugbarn.typescript");
  assert.equal(events[0].exception.value, "boom");
  assert.equal(events[0].tags?.service, "api");
});

test("flush delegates to transport", async () => {
  let flushed = false;
  const transport: Transport = {
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
  const calls: Array<{ url: string; init?: RequestInit }> = [];

  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), init });
    return new Response(null, { status: 200 });
  }) as typeof fetch;

  try {
    const transport = createTransport("bb_live_test", "http://127.0.0.1:9000/api/v1/events");
    await transport.send({
      sdk: "bugbarn.typescript",
      message: "boom",
      exception: { type: "Error", value: "boom" },
      timestamp: new Date().toISOString(),
    });
    await transport.flush();
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "http://127.0.0.1:9000/api/v1/events");
  assert.equal(new Headers(calls[0].init?.headers).get("x-bugbarn-api-key"), "bb_live_test");
});
