// Every view of a release renders its stored url as a link only when it is an
// absolute http(s) URL. The components import the DOM-free helpers by their
// build-time .js names, so esbuild bundles them for this test.

import test from "node:test";
import assert from "node:assert/strict";
import { buildSync } from "esbuild";
import { httpUrl } from "../src/surfaces.ts";

async function load(entry) {
  const out = buildSync({ entryPoints: [entry], bundle: true, format: "esm", platform: "neutral", write: false });
  const source = Buffer.from(out.outputFiles[0].contents).toString("base64");
  return import(`data:text/javascript;base64,${source}`);
}

const { renderReleasesViewMarkup, renderReleaseDetailMarkup } = await load("src/components/releases.ts");
const { renderIssueDetailMarkup } = await load("src/components/issues-detail.ts");

const observedAt = "2026-10-01T09:00:00Z";
const release = (url) => ({ id: "rel_1", name: "v1", environment: "prod", observedAt, url });
const issue = { id: "ISS-1", title: "boom", last_seen: "2026-10-01T10:00:00Z", lastSeen: "2026-10-01T10:00:00Z" };

const views = {
  "the release list": (r) => renderReleasesViewMarkup([r]),
  "the release detail": (r) => renderReleaseDetailMarkup(r, [], [], null),
  "the issue detail": (r) => renderIssueDetailMarkup(issue, [], [r]),
};

const unsafe = [
  "javascript:alert(1)",
  " JavaScript:alert(1)",
  "java\nscript:alert(1)",
  "data:text/html,<script>alert(1)</script>",
  "vbscript:msgbox(1)",
  "/relative/path",
];

for (const [name, render] of Object.entries(views)) {
  test(`${name} links an https url`, () => {
    assert.match(render(release("https://example.com/releases/v1")), /href="https:\/\/example\.com\/releases\/v1"/);
  });

  test(`${name} renders no link for an unsafe url`, () => {
    for (const url of unsafe) {
      const html = render(release(url));
      assert.doesNotMatch(html, /script:/i, url);
      assert.doesNotMatch(html, /href="(data|\/relative)/, url);
    }
  });
}

test("httpUrl keeps http and https and refuses the rest", () => {
  assert.equal(httpUrl("http://example.com/a"), "http://example.com/a");
  assert.equal(httpUrl(" https://example.com/a "), "https://example.com/a");
  for (const value of [...unsafe, "", "   ", null, undefined, 42, {}]) {
    assert.equal(httpUrl(value), null, String(value));
  }
});
