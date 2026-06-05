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

// sameOriginClients returns every window of this origin the service
// worker can see. Used to decide whether to route the push to an
// in-app toast (wick is open somewhere) or surface an OS notification
// (wick is fully in the background — the user has no other channel).
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

  event.waitUntil((async () => {
    const clients = await sameOriginClients();
    const hasClient = clients.length > 0;

    // When wick is open anywhere (focused or not), route via
    // postMessage so the page can render its own in-app toast that
    // navigates within the SPA. We still HAVE to call
    // showNotification because the subscription is userVisibleOnly —
    // but we keep it silent (no sound, no vibration, no banner pin)
    // so the OS surface stays out of the way. The in-app toast is
    // what the user actually sees and interacts with.
    if (hasClient) {
      for (const c of clients) {
        try {
          c.postMessage({
            type: 'wick:lifecycle_push',
            title: title,
            body: body,
            url: targetURL,
          });
        } catch (_) {}
      }
      return self.registration.showNotification(title, {
        body: body,
        icon: '/public/img/icon-192.png',
        badge: '/public/img/icon-192.png',
        data: { url: targetURL },
        silent: true,
        requireInteraction: false,
        tag: 'wick:' + targetURL,
        renotify: false,
      });
    }

    // No wick client at all — fall back to the real OS notification.
    // Click navigates via notificationclick (opens wick + jumps to
    // the session URL).
    return self.registration.showNotification(title, {
      body: body,
      icon: '/public/img/icon-192.png',
      badge: '/public/img/icon-192.png',
      data: { url: targetURL },
      requireInteraction: false,
      tag: 'wick:' + targetURL,
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
