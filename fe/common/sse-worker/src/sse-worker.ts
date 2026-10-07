/// <reference lib="webworker" />
/*
 * Purpose:    SharedWorker — multiplexes EVERY subscribed session onto ONE
 *             /stream/multi EventSource and fans events to subscribed
 *             MessagePorts by session_id; also owns the single lifecycle
 *             (/stream/sessions) stream — session lifecycle for the sidebar
 *             and turn `activity` for the Team roster, plus a `ticket` signal
 *             for open boards and an `agent_changed` signal for the roster. Fetches /stream/snapshot per session
 *             on late-join/reconnect so nothing is missed. Self-heals with
 *             backoff once the browser's own reconnect gives up.
 * Caller:     Instantiated via `new SharedWorker(new URL(...), { type: "module" })`
 * Dependencies: Browser EventSource, fetch
 * Main Functions: self.onconnect handler
 * Side Effects: Opens/closes EventSource connections
 *
 * Why one connection instead of one per session: a browser allows only ~6
 * concurrent HTTP/1.1 connections per origin, and an SSE stream holds its
 * slot for as long as any page subscribes. One-stream-per-session meant four
 * open conversations (plus the lifecycle and dev-reload streams) hit the
 * quota exactly, and every request issued afterwards — an icon, an API call,
 * a navigation — sat unsent in the browser's queue, indistinguishable from a
 * hung server. Multiplexing keeps the cost at ONE connection no matter how
 * many tabs and sessions are open.
 */

/* Ports keyed by sessionID; all sessions share the single mux stream. */
const ports: Record<string, Set<MessagePort>> = {};
/* True once a session has been carried by an OPEN mux stream — a reopen then
   replays its snapshot to fill the gap; the first open doesn't (the panel
   loads history over REST). */
const everConnected: Record<string, boolean> = {};

/* The one multiplexed stream. Rebuilt (debounced) whenever the subscribed
   session set changes — SSE cannot change its query string after open. */
let muxSource: EventSource | null = null;
let muxBase = "";
let muxSids = ""; // sorted, comma-joined set the current source was opened with
let muxReopenTimer: ReturnType<typeof setTimeout> | null = null;
let muxRetryTimer: ReturnType<typeof setTimeout> | null = null;
let muxRetryAttempts = 0;

/* The sidebar's lifecycle stream (/stream/sessions), kept separate because it
   is per-USER, not per-session: every tab wants the same one. */
const lifecyclePorts = new Set<MessagePort>();
let lifecycleSource: EventSource | null = null;
let lifecycleBase = "";
let lifecycleRetryTimer: ReturnType<typeof setTimeout> | null = null;
let lifecycleRetryAttempts = 0;
/* Last `activity` per conversation (what its turn is doing), for a port that
   joins while the stream is already open: the server replays it only to a
   NEW connection. Idle ones are dropped, so this holds live turns only, and
   a reopen clears it because the server replays afresh. */
const lastActivity = new Map<string, unknown>();

function broadcast(sessionID: string, msg: unknown): void {
  const set = ports[sessionID];
  if (!set) return;
  set.forEach((p) => {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
}

function broadcastAll(status: string): void {
  Object.keys(ports).forEach((sid) =>
    broadcast(sid, { type: "status", sessionID: sid, status }),
  );
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

function subscribedSids(): string[] {
  return Object.keys(ports).filter((sid) => ports[sid].size > 0).sort();
}

/* Rebuild the mux stream for the CURRENT session set. Debounced: a page
   mounting subscribes to several sessions in quick succession (main view +
   sub-agent rail), and each reopen costs a connection cycle. */
function scheduleMuxReopen(): void {
  if (muxReopenTimer) return;
  muxReopenTimer = setTimeout(() => {
    muxReopenTimer = null;
    openMux();
  }, 150);
}

function openMux(): void {
  const sids = subscribedSids();
  const key = sids.join(",");
  if (muxSource && key === muxSids && muxSource.readyState !== EventSource.CLOSED) {
    return; // already carrying exactly this set
  }
  try { muxSource?.close(); } catch (_) { /* already gone */ }
  muxSource = null;
  muxSids = key;
  if (!key || !muxBase) return; // nobody listening

  const es = new EventSource(
    `${muxBase}/stream/multi?sessions=${encodeURIComponent(key)}`,
    { withCredentials: true },
  );
  muxSource = es;

  es.addEventListener("agent", (ev: MessageEvent) => {
    let parsed: { session_id?: string };
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    const sid = parsed.session_id;
    if (!sid || !ports[sid]) return;
    broadcast(sid, { type: "event", sessionID: sid, event: parsed });
  });

  es.onopen = () => {
    muxRetryAttempts = 0;
    for (const sid of subscribedSids()) {
      broadcast(sid, { type: "status", sessionID: sid, status: "connected" });
      // Catch up on whatever this session missed while the stream was down
      // or being rebuilt. First-ever open skips it — history comes via REST.
      if (everConnected[sid]) fetchSnapshot(sid, muxBase);
      everConnected[sid] = true;
    }
  };

  es.onerror = () => {
    broadcastAll("error");
    // The browser retries on its own while CONNECTING; only take over once
    // it has fully given up (CLOSED — e.g. server down or a hard drop).
    if (es.readyState === EventSource.CLOSED) scheduleMuxRetry();
  };
}

function scheduleMuxRetry(): void {
  if (muxRetryTimer) return;
  if (subscribedSids().length === 0) return;
  muxRetryAttempts += 1;
  const delay = Math.min(1000 * 2 ** (muxRetryAttempts - 1), 15000); // 1,2,4,8,15s cap
  muxRetryTimer = setTimeout(() => {
    muxRetryTimer = null;
    muxSids = ""; // force a reopen even for an identical set
    openMux();
  }, delay);
}

/* Remove a port everywhere it may be registered. Used by unsubscribe AND by
   the port's own close event (where supported): a reloading or closing page
   often never gets to post its unsubscribes, and a MessagePort emits nothing
   on death in older browsers — those zombie ports used to pin their
   sessions' streams open forever. */
function reapPort(port: MessagePort): void {
  let changed = false;
  for (const sid of Object.keys(ports)) {
    if (ports[sid].delete(port) && ports[sid].size === 0) {
      delete ports[sid];
      delete everConnected[sid];
      changed = true;
    }
  }
  if (changed) scheduleMuxReopen();
  if (lifecyclePorts.delete(port) && lifecyclePorts.size === 0) {
    stopLifecycle();
  }
}

/* ── lifecycle stream ─────────────────────────────────────────────── */

function broadcastLifecycle(msg: unknown): void {
  lifecyclePorts.forEach((p) => {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
}

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
  lastActivity.clear();

  es.addEventListener("session", (ev: MessageEvent) => {
    let parsed: unknown;
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    broadcastLifecycle({ type: "session", event: parsed });
  });

  es.addEventListener("activity", (ev: MessageEvent) => {
    let parsed: { session_id?: string; work?: string; needs_attention?: boolean };
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    if (!parsed || !parsed.session_id) return;
    if (parsed.work || parsed.needs_attention) lastActivity.set(parsed.session_id, parsed);
    else lastActivity.delete(parsed.session_id);
    broadcastLifecycle({ type: "activity", event: parsed });
  });

  /* A ticket was written: a bare {project_id, ticket_id} signal, already
     filtered by project access server-side. Not cached — an open board
     refetches on reconnect anyway. */
  es.addEventListener("ticket", (ev: MessageEvent) => {
    let parsed: { project_id?: string };
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    if (!parsed || !parsed.project_id) return;
    broadcastLifecycle({ type: "ticket", event: parsed });
  });

  /* A Team agent (or group) the user sees was created, edited, shared,
     unshared or deleted: a bare {agent_id} / {group_id} signal, already
     filtered by roster access server-side. Not cached — the roster
     refetches on reconnect anyway. */
  es.addEventListener("agent_changed", (ev: MessageEvent) => {
    let parsed: { agent_id?: string; group_id?: string };
    try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
    if (!parsed || (!parsed.agent_id && !parsed.group_id)) return;
    broadcastLifecycle({ type: "agent_changed", event: parsed });
  });

  /* Something in the pool moved: a bare signal the Overview refetches on. */
  es.addEventListener("pool", () => {
    broadcastLifecycle({ type: "pool" });
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

function stopLifecycle(): void {
  if (lifecycleRetryTimer) { clearTimeout(lifecycleRetryTimer); lifecycleRetryTimer = null; }
  lifecycleRetryAttempts = 0;
  if (lifecycleSource) {
    lifecycleSource.close();
    lifecycleSource = null;
  }
  lastActivity.clear();
}

/* ── port wiring ──────────────────────────────────────────────────── */

(self as unknown as SharedWorkerGlobalScope).onconnect = function (e: MessageEvent) {
  const port = (e as MessageEvent & { ports: MessagePort[] }).ports[0];

  port.onmessage = function (msg: MessageEvent) {
    const data = msg.data as { type?: string; sessionID?: string; base?: string } | null;
    if (!data || !data.type) return;

    if (data.type === "subscribe") {
      const sid = data.sessionID;
      const base = data.base;
      if (sid === undefined || sid === null || !base) return;
      muxBase = base;

      const isNew = !ports[sid] || ports[sid].size === 0;
      if (!ports[sid]) ports[sid] = new Set();
      ports[sid].add(port);

      if (!isNew && muxSource && muxSource.readyState !== EventSource.CLOSED) {
        // The mux already carries this session — this is a late or
        // re-subscribing client (a second tab, or one returning from the
        // background). Give it the current status and a private snapshot
        // replay so it catches up without disturbing the others.
        port.postMessage({
          type: "status",
          sessionID: sid,
          status: muxSource.readyState === EventSource.OPEN ? "connected" : "connecting",
        });
        fetchSnapshot(sid, base, port);
        return;
      }
      scheduleMuxReopen();

    } else if (data.type === "unsubscribe") {
      const sid = data.sessionID;
      if (sid === undefined || sid === null || !ports[sid]) return;
      ports[sid].delete(port);
      if (ports[sid].size === 0) {
        delete ports[sid];
        delete everConnected[sid];
        scheduleMuxReopen();
      }

    } else if (data.type === "subscribe-lifecycle") {
      const base = data.base;
      if (!base) return;
      lifecyclePorts.add(port);
      if (lifecycleSource && lifecycleSource.readyState !== EventSource.CLOSED) {
        port.postMessage({
          type: "lifecycle-status",
          status: lifecycleSource.readyState === EventSource.OPEN ? "connected" : "connecting",
        });
        lastActivity.forEach((ev) => port.postMessage({ type: "activity", event: ev }));
        return;
      }
      connectLifecycle(base);

    } else if (data.type === "unsubscribe-lifecycle") {
      lifecyclePorts.delete(port);
      if (lifecyclePorts.size === 0) stopLifecycle();
    }
  };

  // Where the browser supports it (MessagePort "close" fires when the other
  // side is GC'd or its page dies), reap the port so a reload that never got
  // to post unsubscribe cannot leave its sessions pinned open. Older
  // browsers fall back to the page's pagehide unsubscribes.
  try {
    (port as unknown as { addEventListener(t: string, cb: () => void): void })
      .addEventListener("close", () => reapPort(port));
  } catch (_) { /* not supported */ }

  port.start();
};
