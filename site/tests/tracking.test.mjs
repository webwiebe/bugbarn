// The marketing site loads FunnelBarn and BrandTrace, and reports browser errors
// to BugBarn when the build carries PUBLIC_BUGBARN_SITE_KEY (#281).
// Runs against the built site: `PUBLIC_BUGBARN_SITE_KEY=k pnpm run build && pnpm run test`.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const html = readFileSync(join(dist, 'index.html'), 'utf8');

test('FunnelBarn tag keeps its attributes', () => {
  assert.match(html, /<script[^>]*src="https:\/\/funnelbarn\.wiebe\.xyz\/sdk\.js"[^>]*data-api-key=/);
  assert.match(html, /data-recording="true"/);
});

test('BrandTrace tag carries the site key', () => {
  assert.match(html, /<script[^>]*src="https:\/\/brandtrace\.net\/sdk\.js"[^>]*data-site-key="[0-9a-f]{32}"/);
});

test('error reporter is present exactly when the build has a key', () => {
  const has = html.includes('x-bugbarn-api-key');
  if (process.env.PUBLIC_BUGBARN_SITE_KEY) {
    assert.ok(has, 'reporter missing although the key was set');
    assert.ok(html.includes(process.env.PUBLIC_BUGBARN_SITE_KEY));
  } else {
    assert.ok(!has, 'reporter rendered without a key');
  }
});
