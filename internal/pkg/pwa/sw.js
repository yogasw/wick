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

// isViewingTarget returns true when at least one of this origin's
// windows is focused on the target URL path. Used by the push handler
// to suppress sound + interaction on notifications the user clearly
// doesn't need (they're already looking at the session).
//
// `userVisibleOnly: true` (set when subscribing) means we MUST call
// showNotification for every push or the browser may revoke the
// subscription. The compromise: still show the notification, but with
// `silent: true` and `requireInteraction: false` so it doesn't beep,
// vibrate, or pin to the action center. The OS still records it,
// satisfying the policy.
async function isViewingTarget(targetURL) {
  const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  for (const c of all) {
    if (!c.focused) continue;
    try {
      const u = new URL(c.url);
      if (u.origin !== self.location.origin) continue;
      if (u.pathname === targetURL || u.pathname.startsWith(targetURL + '/')) return true;
    } catch (_) {}
  }
  return false;
}

self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (_) {
    data = { body: event.data ? event.data.text() : '' };
  }

  const title = data.title || 'Wick notification';
  const targetURL = data.url || '/';

  event.waitUntil((async () => {
    const viewing = await isViewingTarget(targetURL);
    const options = {
      body: data.body || '',
      icon: '/public/img/icon-192.png',
      badge: '/public/img/icon-192.png',
      data: { url: targetURL },
      // When the user is already looking at the session URL, don't
      // beep / vibrate / pin to the action center. The push is still
      // delivered (required by userVisibleOnly) but stays out of the
      // user's face.
      silent: viewing,
      requireInteraction: false,
      // Tag groups consecutive lifecycle pushes for the same session
      // so the previous one collapses into the new one (e.g. working
      // → idle within seconds doesn't stack two banners).
      tag: 'wick:' + targetURL,
      renotify: !viewing,
    };
    return self.registration.showNotification(title, options);
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
