import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { createSourceMapUploader, uploadSourceMap } from "../dist/esm/source-maps.js";

// A local server that records each request and answers with a fixed status.
async function withServer(status, run) {
  const requests = [];
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      requests.push({ url: req.url, headers: req.headers, body: Buffer.concat(chunks).toString() });
      res.statusCode = status;
      res.end();
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  try {
    await run(base, requests);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

test("uploadSourceMap sends the project header and a Blob source map", async () => {
  await withServer(202, async (base, requests) => {
    await uploadSourceMap({
      apiKey: "key-1",
      project: "web-app",
      endpoint: `${base}/api/v1/source-maps`,
      release: "2.0.0",
      bundleUrl: "https://example.test/app.js",
      sourceMap: new Blob(["{\"version\":3}"]),
      sourceMapName: "app.js.map",
    });
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/v1/source-maps");
    assert.equal(requests[0].headers["x-bugbarn-api-key"], "key-1");
    assert.equal(requests[0].headers["x-bugbarn-project"], "web-app");
    assert.match(requests[0].body, /name="source_map_name"\r\n\r\napp\.js\.map/);
    assert.match(requests[0].body, /\{"version":3\}/);
    assert.doesNotMatch(requests[0].body, /name="dist"/);
  });
});

test("uploadSourceMap reads the map from sourceMapPath", async () => {
  const dir = await mkdtemp(join(tmpdir(), "bb-sdk-"));
  const file = join(dir, "bundle.js.map");
  await writeFile(file, "{\"version\":3,\"from\":\"disk\"}");
  await withServer(200, async (base, requests) => {
    await uploadSourceMap({
      apiKey: "key-1",
      endpoint: `${base}/api/v1/source-maps`,
      release: "2.0.0",
      bundleUrl: "https://example.test/bundle.js",
      sourceMapPath: file,
    });
    assert.match(requests[0].body, /"from":"disk"/);
    assert.match(requests[0].body, /filename="source\.map"/);
    assert.equal(requests[0].headers["x-bugbarn-project"], undefined);
  });
});

test("uploadSourceMap sends an empty map when none is given", async () => {
  await withServer(200, async (base, requests) => {
    await uploadSourceMap({
      apiKey: "key-1",
      endpoint: `${base}/api/v1/source-maps`,
      release: "2.0.0",
      bundleUrl: "https://example.test/bundle.js",
    });
    assert.match(requests[0].body, /name="source_map"; filename="source\.map"/);
  });
});

test("uploadSourceMap rejects with the status when the server refuses", async () => {
  await withServer(500, async (base) => {
    await assert.rejects(
      uploadSourceMap({
        apiKey: "key-1",
        endpoint: `${base}/api/v1/source-maps`,
        release: "2.0.0",
        bundleUrl: "https://example.test/bundle.js",
        sourceMap: "{}",
      }),
      /failed with 500/,
    );
  });
});

test("uploadSourceMap resolves a relative endpoint against the loopback host", async () => {
  const originalFetch = globalThis.fetch;
  const urls = [];
  globalThis.fetch = async (input) => {
    urls.push(String(input));
    return new Response(null, { status: 200 });
  };
  try {
    const base = { apiKey: "k", release: "1", bundleUrl: "https://x.test/a.js", sourceMap: "{}" };
    await uploadSourceMap({ ...base, endpoint: "/custom/maps" });
    await uploadSourceMap({ ...base, endpoint: "custom/maps" });
    await uploadSourceMap(base);
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(urls, [
    "http://127.0.0.1/custom/maps",
    "http://127.0.0.1/custom/maps",
    "http://127.0.0.1/api/v1/source-maps",
  ]);
});

test("createSourceMapUploader fills in the shared configuration", async () => {
  await withServer(200, async (base, requests) => {
    const upload = createSourceMapUploader({
      apiKey: "key-2",
      endpoint: `${base}/api/v1/source-maps`,
      release: "3.1.0",
      dist: "ios",
      project: "mobile",
    });
    await upload({ bundleUrl: "https://example.test/main.js", sourceMap: "{\"v\":1}", sourceMapName: "main.map" });
    await upload({ bundleUrl: "https://example.test/main.js" });
    assert.equal(requests.length, 2);
    assert.equal(requests[0].headers["x-bugbarn-api-key"], "key-2");
    assert.equal(requests[0].headers["x-bugbarn-project"], "mobile");
    assert.match(requests[0].body, /name="release"\r\n\r\n3\.1\.0/);
    assert.match(requests[0].body, /name="dist"\r\n\r\nios/);
  });
});
