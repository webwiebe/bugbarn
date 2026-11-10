// Every link in the landing nav must resolve on the landing host (#273).
// Runs against the built site: `pnpm run build && pnpm run test`.
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const landing = new URL('https://bugbarn.wiebe.xyz/');
// Paths the landing host serves from another app, not from this build.
const servedElsewhere = new Set(['/app/']);

function navHrefs(html) {
  const nav = html.match(/<nav[\s\S]*?<\/nav>/);
  assert.ok(nav, 'index.html has a <nav>');
  return [...nav[0].matchAll(/<a\s[^>]*?href="([^"]*)"/g)].map((m) => m[1]);
}

test('every landing nav href resolves on the landing host', () => {
  const html = readFileSync(join(dist, 'index.html'), 'utf8');
  const hrefs = navHrefs(html);
  assert.ok(hrefs.length >= 5, `expected the nav links, got ${hrefs.length}`);

  for (const href of hrefs) {
    const url = new URL(href, landing);
    if (url.host !== landing.host) {
      assert.ok(!href.startsWith('//'), `${href} is protocol-relative and points at host "${url.host}"`);
      continue;
    }
    if (url.hash && url.pathname === '/') {
      assert.ok(html.includes(`id="${url.hash.slice(1)}"`), `${href} has no matching id on the landing page`);
      continue;
    }
    if (servedElsewhere.has(url.pathname)) continue;
    const file = join(dist, url.pathname.endsWith('/') ? `${url.pathname}index.html` : url.pathname);
    assert.ok(existsSync(file), `${href} resolves to ${url.pathname}, which the build does not produce`);
  }
});
