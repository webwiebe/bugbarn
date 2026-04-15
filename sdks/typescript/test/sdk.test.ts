import assert from "node:assert/strict";
import test from "node:test";

import { captureException, createTransport, flush, getApiKey, init } from "../src/index.ts";
import type { BugBarnEnvelope, Transport } from "../src/types.ts";

test("init stores transport and api key", async () => {
  const events: BugBarnEnvelope[] = [];
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
  assert.equal(events[0].severityText, "ERROR");
  assert.equal(events[0].body, "boom");
  assert.equal(events[0].exception.type, "Error");
  assert.equal(events[0].exception.message, "boom");
  assert.equal(events[0].tags?.service, "api");
  assert.equal(events[0].sender.sdk.name, "bugbarn.typescript");
  assert.equal(events[0].sender.sdk.version, "0.1.0");
  assert.ok(events[0].timestamp.length > 0);
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
  const body = JSON.parse(String(calls[0].init?.body)) as BugBarnEnvelope;
  assert.equal(body.body, "boom");
  assert.equal(body.exception.message, "boom");
  assert.equal(body.sender.sdk.name, "bugbarn.typescript");
});
