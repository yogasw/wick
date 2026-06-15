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
// can see (focused or not, visible or not). The push handler narrows this
// down to decide whether to surface an OS notification.
async function sameOriginClients() {
  const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  return all.filter((c) => {
    try { return new URL(c.url).origin === self.location.origin; }
    catch (_) { return false; }
  });
}

// urlPath returns the pathname of a URL (absolute, or relative to this
// origin), or '' when it can't be parsed. Used to compare the page a push
// points at against the pages the user currently has open.
function urlPath(u) {
  try { return new URL(u, self.location.origin).pathname; }
  catch (_) { return ''; }
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

    // Suppress the OS notification ONLY when the user is actively looking
    // at the very page this push is about — i.e. a *visible* wick window
    // already on the target path. In that one case the page shows its own
    // in-app toast, so an OS notification would just duplicate what's
    // already on screen.
    //
    // Every other case surfaces a real OS notification: wick focused on a
    // different page/menu, wick only in a background tab, or wick fully
    // closed. So looking away from (or closing) the relevant tab still
    // notifies; only staying on that exact page stays silent.
    const targetPath = urlPath(targetURL);
    const onTarget = clients.filter(
      (c) => c.visibilityState === 'visible' && urlPath(c.url) === targetPath
    );

    if (onTarget.length > 0) {
      for (const c of onTarget) {
        try {
          c.postMessage({
            type: 'wick:lifecycle_push',
            title: title,
            body: body,
            url: targetURL,
          });
        } catch (_) {}
      }
      // userVisibleOnly: true (subscription constraint) requires us to call
      // showNotification for every push. Satisfy the spec by showing one
      // silently then closing it immediately — the user only ever sees the
      // in-app toast.
      await self.registration.showNotification(title, {
        body: body,
        icon: '/public/img/icon-192.png',
        badge: '/public/img/icon-badge.png',
        data: { url: targetURL },
        silent: true,
        requireInteraction: false,
        tag: tag,
        renotify: false,
      });
      try {
        const notes = await self.registration.getNotifications({ tag: tag });
        notes.forEach((n) => { try { n.close(); } catch (_) {} });
      } catch (_) {}
      return;
    }

    // User isn't looking at the target page — surface a real OS
    // notification. Click navigates via notificationclick (focuses an
    // existing window + jumps to the session URL, or opens a new one).
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
