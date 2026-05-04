// Cache version is stamped at Docker build time from a hash of the compiled
// assets. Any change to dist/app.js or styles.css produces a new hash, which
// makes the browser treat this as a new service worker, triggering install →
// activate → old cache deletion. Never edit __BUILD_HASH__ by hand.
const CACHE_VERSION = '__BUILD_HASH__';
const CACHE_NAME = `bugbarn-${CACHE_VERSION}`;
const API_CACHE = `bugbarn-api-${CACHE_VERSION}`;

const APP_SHELL = ['/', '/dist/app.js', '/styles.css', '/manifest.json'];

const CACHEABLE_API = [
  '/api/v1/projects',
  '/api/v1/facets/attributes.environment',
  '/api/v1/apikeys',
];

function isCacheableApi(url) {
  const path = new URL(url).pathname;
  return CACHEABLE_API.some((p) => path === p);
}

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
        Promise.all(keys.filter((k) => k !== CACHE_NAME && k !== API_CACHE).map((k) => caches.delete(k)))
      )
      .then(() => self.clients.claim())
  );
});

// Fetch: network-first for app shell, stale-while-revalidate for cacheable
// API endpoints, pass-through for everything else.
self.addEventListener('fetch', (event) => {
  const url = event.request.url;

  // Pass through cross-origin and non-GET requests.
  if (!url.startsWith(self.location.origin)) return;
  if (event.request.method !== 'GET') return;

  // Stale-while-revalidate for cacheable API endpoints.
  if (isCacheableApi(url)) {
    event.respondWith(
      caches.open(API_CACHE).then((cache) =>
        cache.match(event.request).then((cached) => {
          const networkFetch = fetch(event.request).then((response) => {
            if (response.ok) cache.put(event.request, response.clone());
            return response;
          });
          return cached || networkFetch;
        })
      )
    );
    return;
  }

  // Pass through non-cacheable API requests.
  if (url.includes('/api/')) return;

  // Network-first for app shell and other same-origin resources.
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

// Handle notification clicks — open the associated issue page.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = event.notification.data?.url;
  if (url) {
    event.waitUntil(
      self.clients.matchAll({ type: 'window' }).then((clients) => {
        for (const client of clients) {
          if (client.url.includes(self.location.origin) && 'focus' in client) {
            client.navigate(url);
            return client.focus();
          }
        }
        return self.clients.openWindow(url);
      })
    );
  }
});
