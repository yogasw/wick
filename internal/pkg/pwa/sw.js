// wick service worker — minimal, enables PWA installability and a small
// static cache. Kept conservative on purpose: navigations always go to the
// network so we never serve a stale Cloudflare Access login page from cache.
const CACHE = 'wick-static-v4';
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

// staticAssetRe matches static assets we cache: images, styles, scripts,
// fonts. NOT all of these are immutable — only Vite's hashed entry bundles
// (index-<hash>.js, anything under assets/) never change under a given URL.
// The rest (app.css, app.js, dialog.js, palette.js, push.js, …) keep a STABLE
// url but their CONTENT changes every deploy. A plain cache-first pins those
// to whatever shipped first and never picks up a new deploy until a hard
// refresh — that was the "stale layout until Ctrl+Shift+R" bug. So we serve
// them stale-while-revalidate instead: instant from cache, but always refetch
// in the background so the NEXT load is fresh.
const staticAssetRe = /\.(?:svg|png|jpg|jpeg|gif|webp|ico|css|js|mjs|woff2?|ttf|eot)$/i;

// Routing:
//   - static assets → stale-while-revalidate (instant from cache + background
//     refresh, so a new deploy surfaces on the next load — no hard refresh)
//   - everything else (navigations, JSON APIs) → network-first, cache as
//     offline fallback. Navigations must never serve a stale Cloudflare Access
//     login page from cache, so they stay network-first.
// Cross-origin requests are NOT intercepted — letting them go straight to the
// network avoids needless proxying and the "Failed to convert value to
// 'Response'" crash when such a fetch failed and respondWith() got undefined.
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  let url;
  try { url = new URL(req.url); }
  catch (_) { return; }
  if (url.origin !== self.location.origin) return;

  if (staticAssetRe.test(url.pathname)) {
    // Stale-while-revalidate: return the cached copy immediately (or fall back
    // to the network on a cold miss), and ALWAYS kick off a network fetch in
    // the background to refresh the cache for next time. A failed background
    // fetch is swallowed so an offline reload still serves the cached copy.
    event.respondWith(
      caches.match(req).then((cached) => {
        const fetching = fetch(req).then((res) => {
          if (res && res.ok) {
            const copy = res.clone();
            caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
          }
          return res;
        }).catch(() => cached);
        return cached || fetching;
      })
    );
    return;
  }

  // Network-first for navigations and dynamic responses.
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
