/// <reference lib="webworker" />
/*
 * Purpose:    SharedWorker — holds one EventSource per sessionID, fans events
 *             to subscribed MessagePorts, fetches /stream/snapshot on each
 *             new subscribe. Self-heals: reconnects when the stream drops and
 *             replays any events missed during the gap. Shared by conversation
 *             and scm SPAs.
 * Caller:     Instantiated via `new SharedWorker(new URL(...), { type: "module" })`
 * Dependencies: Browser EventSource, fetch
 * Main Functions: self.onconnect handler
 * Side Effects: Opens/closes EventSource connections per session
 */

/* Ports and EventSources keyed by sessionID. */
const ports: Record<string, Set<MessagePort>> = {};
const sources: Record<string, EventSource> = {};

/* The sidebar's lifecycle stream (/stream/sessions) lives here too, keyed
   separately because it is per-USER, not per-session: every tab wants the same
   one. Routing it through the worker is what keeps it to a single connection
   no matter how many tabs are open.

   Why it matters beyond tidiness: a browser allows only ~6 concurrent HTTP/1.1
   connections per origin, and an SSE stream holds its slot for as long as the
   tab lives. Two tabs each opening their own lifecycle stream plus a per-session
   stream exhausted the quota, and every request made afterwards — an API call,
   an icon, a navigation — sat unsent in the browser's queue looking exactly
   like a hung server. */
const lifecyclePorts = new Set<MessagePort>();
let lifecycleSource: EventSource | null = null;
let lifecycleBase = "";
let lifecycleRetryTimer: ReturnType<typeof setTimeout> | null = null;
let lifecycleRetryAttempts = 0;
/* Remembered per sid so a background reconnect can rebuild the stream and
   refetch the snapshot without another subscribe from the page. */
const bases: Record<string, string> = {};
/* True once a session's stream has opened at least once — a *re*open then
   replays the snapshot to catch up; the first open doesn't (the panel loads
   history over REST). */
const everConnected: Record<string, boolean> = {};
/* Backoff bookkeeping for manual reconnects. */
const retryTimers: Record<string, ReturnType<typeof setTimeout>> = {};
const retryAttempts: Record<string, number> = {};

function broadcast(sessionID: string, msg: unknown): void {
  const set = ports[sessionID];
  if (!set) return;
  set.forEach((p) => {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
}

/* Replay the in-memory event buffer. `target` sends to a single port (a late
   or re-subscribing client); omit it to fan out to every subscriber (used
   after a reconnect, where all subscribers missed the same gap). */
function fetchSnapshot(sid: string, base: string, target?: MessagePort): void {
  fetch(`${base}/stream/snapshot?session=${encodeURIComponent(sid)}`, { credentials: "include" })
    .then((r) => r.json())
    .then((events: unknown[]) => {
      events.forEach((ev) => {
        const msg = { type: "event", sessionID: sid, event: ev };
        if (target) { try { target.postMessage(msg); } catch (_) { /* port gone */ } }
        else broadcast(sid, msg);
      });
    })
    .catch(() => {});
}

/* Close the current source and reopen it after an exponential backoff. Only
   armed when the browser's own reconnect has given up (readyState CLOSED). */
function scheduleReconnect(sid: string): void {
  if (retryTimers[sid]) return;                 // one pending reconnect at a time
  if (!ports[sid] || ports[sid].size === 0) return; // no one listening
  const attempt = (retryAttempts[sid] ?? 0) + 1;
  retryAttempts[sid] = attempt;
  const delay = Math.min(1000 * 2 ** (attempt - 1), 15000); // 1,2,4,8,15s cap
  retryTimers[sid] = setTimeout(() => {
    delete retryTimers[sid];
    if (!ports[sid] || ports[sid].size === 0) return;
    const base = bases[sid];
    if (!base) return;
    try { sources[sid]?.close(); } catch (_) { /* already gone */ }
    connect(sid, base);
  }, delay);
}

function connect(sid: string, base: string): void {
  bases[sid] = base;
  const es = new EventSource(`${base}/stream?session=${encodeURIComponent(sid)}`, { withCredentials: true });
  sources[sid] = es;

  es.addEventListener("agent", (ev: MessageEvent) => {
    let parsed: unknown;
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    broadcast(sid, { type: "event", sessionID: sid, event: parsed });
  });

  es.onopen = () => {
    retryAttempts[sid] = 0;
    broadcast(sid, { type: "status", sessionID: sid, status: "connected" });
    if (everConnected[sid]) fetchSnapshot(sid, base); // catch up on the gap
    everConnected[sid] = true;
  };

  es.onerror = () => {
    broadcast(sid, { type: "status", sessionID: sid, status: "error" });
    // The browser retries on its own while CONNECTING; only take over once it
    // has fully given up (CLOSED — e.g. server down or a hard drop).
    if (es.readyState === EventSource.CLOSED) scheduleReconnect(sid);
  };
}

function broadcastLifecycle(msg: unknown): void {
  lifecyclePorts.forEach((p) => {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
}

/* Same backoff shape as the per-session streams: only armed once the browser's
   own reconnect has given up (CLOSED). */
function scheduleLifecycleReconnect(): void {
  if (lifecycleRetryTimer) return;
  if (lifecyclePorts.size === 0) return;
  lifecycleRetryAttempts += 1;
  const delay = Math.min(1000 * 2 ** (lifecycleRetryAttempts - 1), 15000);
  lifecycleRetryTimer = setTimeout(() => {
    lifecycleRetryTimer = null;
    if (lifecyclePorts.size === 0) return;
    try { lifecycleSource?.close(); } catch (_) { /* already gone */ }
    lifecycleSource = null;
    connectLifecycle(lifecycleBase);
  }, delay);
}

function connectLifecycle(base: string): void {
  lifecycleBase = base;
  const es = new EventSource(`${base}/stream/sessions`, { withCredentials: true });
  lifecycleSource = es;

  es.addEventListener("session", (ev: MessageEvent) => {
    let parsed: unknown;
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    broadcastLifecycle({ type: "session", event: parsed });
  });

  es.onopen = () => {
    lifecycleRetryAttempts = 0;
    broadcastLifecycle({ type: "lifecycle-status", status: "connected" });
  };

  es.onerror = () => {
    broadcastLifecycle({ type: "lifecycle-status", status: "error" });
    if (es.readyState === EventSource.CLOSED) scheduleLifecycleReconnect();
  };
}

(self as unknown as SharedWorkerGlobalScope).onconnect = function (e: MessageEvent) {
  const port = (e as MessageEvent & { ports: MessagePort[] }).ports[0];

  port.onmessage = function (msg: MessageEvent) {
    const data = msg.data as { type?: string; sessionID?: string; base?: string } | null;
    if (!data || !data.type) return;

    if (data.type === "subscribe") {
      const sid = data.sessionID;
      const base = data.base;
      if (sid === undefined || sid === null || !base) return;

      if (!ports[sid]) ports[sid] = new Set();
      ports[sid].add(port);

      const src = sources[sid];
      if (src && src.readyState !== EventSource.CLOSED) {
        // A live stream already exists — this is a late or re-subscribing
        // client (e.g. a second tab, or one returning from the background).
        // Give it the current status and a private snapshot replay so it
        // catches up without disturbing the other subscribers.
        port.postMessage({
          type: "status",
          sessionID: sid,
          status: src.readyState === EventSource.OPEN ? "connected" : "connecting",
        });
        fetchSnapshot(sid, base, port);
        return;
      }

      // No live source (first subscriber, or the previous one closed/died):
      // open a fresh stream that self-heals from here on.
      connect(sid, base);

    } else if (data.type === "subscribe-lifecycle") {
      const base = data.base;
      if (!base) return;
      lifecyclePorts.add(port);
      // A live stream already exists — the newcomer just joins it. No snapshot
      // replay: the endpoint replays what is running on connect, and the page
      // paints its dots from the server-rendered HTML, so there is no gap to
      // fill for a late subscriber.
      if (lifecycleSource && lifecycleSource.readyState !== EventSource.CLOSED) {
        port.postMessage({
          type: "lifecycle-status",
          status: lifecycleSource.readyState === EventSource.OPEN ? "connected" : "connecting",
        });
        return;
      }
      connectLifecycle(base);

    } else if (data.type === "unsubscribe-lifecycle") {
      lifecyclePorts.delete(port);
      if (lifecyclePorts.size === 0) {
        if (lifecycleRetryTimer) { clearTimeout(lifecycleRetryTimer); lifecycleRetryTimer = null; }
        lifecycleRetryAttempts = 0;
        if (lifecycleSource) {
          lifecycleSource.close();
          lifecycleSource = null;
        }
      }

    } else if (data.type === "unsubscribe") {
      const sid = data.sessionID;
      if (sid === undefined || sid === null || !ports[sid]) return;
      ports[sid].delete(port);
      if (ports[sid].size === 0) {
        delete ports[sid];
        if (retryTimers[sid]) { clearTimeout(retryTimers[sid]); delete retryTimers[sid]; }
        delete retryAttempts[sid];
        delete everConnected[sid];
        delete bases[sid];
        if (sources[sid]) {
          sources[sid].close();
          delete sources[sid];
        }
      }
    }
  };

  port.start();
};
