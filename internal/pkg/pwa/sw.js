// wick service worker — minimal, enables PWA installability and a small
// static cache. Kept conservative on purpose: navigations always go to the
// network so we never serve a stale Cloudflare Access login page from cache.
const CACHE = 'wick-static-v2';
const ASSETS = [
  '/public/img/icon-192.png',
  '/public/img/icon-512.png',
  '/public/img/icon-maskable-512.png',
  '/public/img/icon-badge.png',
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

// Network-first for same-origin GETs; the precached static assets fall back
// to cache when offline. Cross-origin requests (Cloudflare beacon, analytics,
// CDNs…) are NOT intercepted — letting them go straight to the network avoids
// needless proxying and the "Failed to convert value to 'Response'" crash that
// happened when such a fetch failed and respondWith() got an undefined cache
// miss.
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  let sameOrigin = false;
  try { sameOrigin = new URL(req.url).origin === self.location.origin; }
  catch (_) { sameOrigin = false; }
  if (!sameOrigin) return;
  event.respondWith(
    fetch(req).catch(() =>
      caches.match(req).then((cached) => cached || Response.error())
    )
  );
});

// sameOriginClients returns every window of this origin the service worker
// can see (focused or not, visible or not). The push handler relays the
// in-app lifecycle card to all of them.
async function sameOriginClients() {
  const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  return all.filter((c) => {
    try { return new URL(c.url).origin === self.location.origin; }
    catch (_) { return false; }
  });
}

self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_) {
    data = { body: event.data ? event.data.text() : '' };
  }

  const title = data.title || 'Wick notification';
  const body = data.body || '';
  const targetURL = data.url || '/';
  const tag = 'wick:' + targetURL;

  event.waitUntil((async () => {
    const clients = await sameOriginClients();

    // Relay the push to EVERY open wick tab so each renders its own in-app
    // card — regardless of which page the tab is on or whether it's
    // visible. The page side keys cards by `tag`, so a repeat collapses
    // instead of stacking, and a user dismiss in one tab is mirrored to
    // the others (BroadcastChannel) — no need to close it N times.
    for (const c of clients) {
      try {
        c.postMessage({
          type: 'wick:lifecycle_push',
          tag: tag,
          title: title,
          body: body,
          url: targetURL,
        });
      } catch (_) {}
    }

    // Always surface exactly one OS notification too. `tag` collapses
    // repeated pushes to the same target into a single OS surface
    // (renotify re-alerts) so it can never stack into spam; the page
    // closes it via getNotifications(tag) when the card is dismissed.
    // Click navigates via notificationclick (focuses an existing window
    // + jumps to the session URL, or opens a new one).
    return self.registration.showNotification(title, {
      body: body,
      icon: '/public/img/icon-192.png',
      badge: '/public/img/icon-badge.png',
      data: { url: targetURL },
      requireInteraction: false,
      tag: tag,
      renotify: true,
    });
  })());
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const targetURL = event.notification.data && event.notification.data.url
    ? event.notification.data.url
    : '/';

  event.waitUntil((async () => {
    const allClients = await clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of allClients) {
      const url = new URL(client.url);
      if (url.origin === self.location.origin) {
        await client.focus();
        if ('navigate' in client) return client.navigate(targetURL);
        return;
      }
    }
    return clients.openWindow(targetURL);
  })());
});
