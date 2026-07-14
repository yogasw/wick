// wick service worker — minimal, enables PWA installability and a small
// static cache. Kept conservative on purpose: navigations always go to the
// network so we never serve a stale Cloudflare Access login page from cache.
const CACHE = 'wick-static-v7';
const ASSETS = [
  '/public/img/icon.svg',
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

  // Server-Sent Events / EventSource streams must NEVER go through the SW. The
  // network-first path below wraps every fetch in an 8s AbortController (to
  // rescue navigations stuck on a dead keep-alive socket); on a long-lived
  // stream that timer fires mid-stream and aborts it every 8s, so the client
  // sees ERR_FAILED and reconnects in a loop — the "logs keep dying" symptom on
  // /reqstream, /logstream, and any SSE endpoint reached by a page-level
  // EventSource (the conversation /stream survives only because it runs in a
  // SharedWorker the page SW can't intercept). EventSource always sends
  // `Accept: text/event-stream`; let those go straight to the network.
  if ((req.headers.get('Accept') || '').includes('text/event-stream')) return;

  // /airouter/* is a reverse-proxied third-party app (an embedded AI-router
  // dashboard — 9router, OmniRoute, …). Its assets are rewritten on the fly
  // by the wick proxy, so caching them here would pin a stale, pre-rewrite
  // copy and break the app. Always go straight to the network — let the proxy
  // be the source of truth.
  //
  // /_next/* is the SAME app's asset namespace: a router's Next.js bundle
  // emits some root-absolute /_next/ URLs at runtime that land here at the
  // wick root (the server re-proxies them to the active dashboard). They match
  // staticAssetRe below, so without this skip the SW would try to cache them
  // and, on a cold miss, serve a stale 404 "from service worker" — exactly
  // the /_next/*.js|css|woff2 404s. Bypass so they reach the server proxy.
  if (
    url.pathname === '/airouter' ||
    url.pathname.endsWith('/airouter') ||
    url.pathname.includes('/airouter/') ||
    url.pathname.startsWith('/_next/')
  ) return;

  // The agents Process panel refreshes /sessions/<id>/processes on every SSE
  // lifecycle transition. Routing those through the SW's network-first path
  // gives each one a duplicate "(from sw.js)" entry in DevTools and adds a
  // proxy hop for a request that must always be live (never cached). Let it
  // go straight to the network — the panel is SSE-driven and short-lived.
  if (url.pathname.endsWith('/processes')) return;

  if (staticAssetRe.test(url.pathname)) {
    // Stale-while-revalidate: return the cached copy immediately (or fall back
    // to the network on a cold miss), and ALWAYS kick off a network fetch in
    // the background to refresh the cache for next time. A failed background
    // fetch is swallowed so an offline reload still serves the cached copy.
    //
    // The background fetch is given a hard timeout. Without it, a stalled
    // connection (dead keep-alive socket, a momentarily busy server) leaves
    // fetch() hanging forever; on a COLD cache there is nothing to fall back
    // to, so respondWith() never settles and the asset sticks at "pending"
    // indefinitely — the random first-load hangs. AbortController bounds it so
    // a stall fails fast and we serve cache (or surface a real error) instead.
    event.respondWith(
      caches.match(req).then((cached) => {
        const ctrl = new AbortController();
        const timer = setTimeout(() => ctrl.abort(), 8000);
        const fetching = fetch(req, { signal: ctrl.signal }).then((res) => {
          clearTimeout(timer);
          if (res && res.ok) {
            const copy = res.clone();
            caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
          }
          return res;
        }).catch(() => {
          clearTimeout(timer);
          return cached;
        });
        // On a cache HIT we answer instantly with `cached`, but the background
        // refresh must be kept alive past respondWith() — extend the event's
        // lifetime so Chrome doesn't tear the fetch down (or leave it dangling)
        // the moment we've answered.
        if (cached) event.waitUntil(fetching);
        return cached || fetching;
      })
    );
    return;
  }

  // Network-first for navigations and dynamic responses.
  //
  // The fetch is bounded by a timeout. A `go run` server reuses HTTP
  // keep-alive sockets; when Go closes an idle connection that Chrome still
  // believes is live, the next request on that dead socket stalls at the TCP
  // layer for tens of seconds with NO error — fetch neither resolves nor
  // rejects, so .catch never fires and the navigation hangs at "pending"
  // forever. That is the "server's been up for ages, then suddenly stuck"
  // case. AbortController converts the stall into a rejection so we fall back
  // to cache (or surface a real error) instead of hanging indefinitely.
  event.respondWith((async () => {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 8000);
    try {
      return await fetch(req, { signal: ctrl.signal });
    } catch (_) {
      const cached = await caches.match(req);
      return cached || Response.error();
    } finally {
      clearTimeout(timer);
    }
  })());
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
