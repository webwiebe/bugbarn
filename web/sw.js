// Cache version is stamped at Docker build time from a hash of the compiled
// assets. Any change to dist/app.js or styles.css produces a new hash, which
// makes the browser treat this as a new service worker, triggering install →
// activate → old cache deletion. Never edit __BUILD_HASH__ by hand.
const CACHE_VERSION = '__BUILD_HASH__';
const CACHE_NAME = `bugbarn-${CACHE_VERSION}`;

const APP_SHELL = ['/', '/dist/app.js', '/styles.css', '/manifest.json'];

// Install: cache the app shell and skip waiting so this SW activates
// immediately without waiting for existing tabs to close.
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME)
      .then((cache) => cache.addAll(APP_SHELL))
      .then(() => self.skipWaiting())
  );
});

// Activate: delete every cache that isn't the current version, then claim
// all open clients so they switch to this SW without a reload.
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) =>
        Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
      )
      .then(() => self.clients.claim())
  );
});

// Fetch: network-first. Always try the network; fall back to cache only if
// the network is unavailable. API calls are never intercepted.
self.addEventListener('fetch', (event) => {
  const url = event.request.url;

  // Pass through cross-origin and API requests untouched.
  if (!url.startsWith(self.location.origin)) return;
  if (url.includes('/api/')) return;
  if (event.request.method !== 'GET') return;

  event.respondWith(
    fetch(event.request)
      .then((response) => {
        if (response.ok) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
        }
        return response;
      })
      .catch(() => caches.match(event.request))
  );
});
