// wick service worker — minimal, enables PWA installability and a small
// static cache. Kept conservative on purpose: navigations always go to the
// network so we never serve a stale Cloudflare Access login page from cache.
const CACHE = 'wick-static-v1';
const ASSETS = [
  '/public/img/icon-192.png',
  '/public/img/icon-512.png',
  '/public/img/icon-maskable-512.png',
  '/public/manifest.json',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => cache.addAll(ASSETS)).catch(() => {})
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

// Network-first. Only the precached static assets fall back to cache when
// offline; everything else (HTML, API) just fails like a normal fetch.
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  event.respondWith(fetch(req).catch(() => caches.match(req)));
});
