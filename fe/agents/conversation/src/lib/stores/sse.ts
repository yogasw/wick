/*
 * Purpose:    Session stream connector — wraps SharedWorker (or EventSource
 *             fallback) into a typed Svelte-store surface. Delivers AgentEvents
 *             via per-event callbacks rather than an accumulating array: callers
 *             react immediately without holding unbounded historical state.
 *             Both transports self-heal: a dropped stream reconnects and the
 *             missed events are replayed from the snapshot.
 * Caller:     Conversation panels / Slice 7 rendering layer
 * Dependencies: svelte/store, AgentEvent, SSEStatus, sse-worker.ts (SharedWorker)
 * Main Functions: connectSession
 * Side Effects: Opens SharedWorker or EventSource connections
 */

import { writable } from "svelte/store";
import type { Readable } from "svelte/store";
import type { AgentEvent, SSEStatus } from "../types/agents.js";

/* Transport abstraction — injectable for tests. */
export interface SSETransport {
  post(msg: unknown): void;
  onMessage(cb: (msg: unknown) => void): void;
  /* Nudge the transport to re-verify its connection and replay any missed
     events (e.g. after the tab returns from the background). Optional: the
     worker path recovers via a re-posted subscribe instead. */
  resync?(): void;
  close(): void;
}

export interface SessionStream {
  /* Register a callback invoked for each incoming AgentEvent. */
  onEvent(cb: (e: AgentEvent) => void): void;
  status: Readable<SSEStatus>;
  /* Re-verify the live connection and replay anything missed. Safe to call
     repeatedly (e.g. on visibilitychange). */
  resync(): void;
  close(): void;
}

function makeWorkerTransport(base: string, sessionID: string): SSETransport {
  if (typeof SharedWorker !== "undefined") {
    const worker = new SharedWorker(
      new URL("@wick-fe/common-sse-worker/src/sse-worker.ts", import.meta.url),
      { type: "module" }
    );
    const port = worker.port;
    port.start();
    return {
      post(msg) { port.postMessage(msg); },
      onMessage(cb) { port.onmessage = (e) => cb(e.data); },
      close() { port.close(); },
    };
  }

  /* Fallback: direct EventSource for environments without SharedWorker. Made
     self-healing to match the worker: reconnect once the browser gives up
     (CLOSED) and replay the snapshot on every re-open. */
  let handler: ((msg: unknown) => void) | null = null;
  let es: EventSource | null = null;
  let everOpen = false;
  let retry = 0;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let fbClosed = false;

  function fbSnapshot(): void {
    fetch(`${base}/stream/snapshot?session=${encodeURIComponent(sessionID)}`, { credentials: "include" })
      .then((r) => r.json())
      .then((events: unknown[]) => {
        events.forEach((ev) => handler?.({ type: "event", sessionID, event: ev }));
      })
      .catch(() => {});
  }

  function fbConnect(): void {
    const src = new EventSource(
      `${base}/stream?session=${encodeURIComponent(sessionID)}`,
      { withCredentials: true }
    );
    es = src;

    src.addEventListener("agent", (ev: MessageEvent) => {
      let parsed: AgentEvent;
      try { parsed = JSON.parse(ev.data as string) as AgentEvent; } catch (_) { return; }
      handler?.({ type: "event", sessionID, event: parsed });
    });

    src.onopen = () => {
      retry = 0;
      handler?.({ type: "status", sessionID, status: "connected" });
      if (everOpen) fbSnapshot();
      everOpen = true;
    };

    src.onerror = () => {
      handler?.({ type: "status", sessionID, status: "error" });
      if (src.readyState === EventSource.CLOSED && !fbClosed && !retryTimer) {
        retry += 1;
        const delay = Math.min(1000 * 2 ** (retry - 1), 15000);
        retryTimer = setTimeout(() => { retryTimer = null; if (!fbClosed) fbConnect(); }, delay);
      }
    };
  }
  fbConnect();

  return {
    post(msg) {
      const m = msg as { type: string };
      if (m.type === "unsubscribe") {
        fbClosed = true;
        if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
        es?.close();
      }
      // Repeat "subscribe" posts are no-ops here; resync() handles recovery.
    },
    onMessage(cb) { handler = cb; },
    resync() {
      if (fbClosed) return;
      if (!es || es.readyState === EventSource.CLOSED) {
        if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
        fbConnect();
      } else {
        fbSnapshot();
      }
    },
    close() {
      fbClosed = true;
      if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
      es?.close();
    },
  };
}

export function connectSession(
  base: string,
  sessionId: string,
  opts?: { transport?: SSETransport }
): SessionStream {
  const transport = opts?.transport ?? makeWorkerTransport(base, sessionId);
  const statusStore = writable<SSEStatus>("connecting");
  const eventCbs = new Set<(e: AgentEvent) => void>();
  let closed = false;

  transport.onMessage((raw) => {
    if (closed) return;
    const msg = raw as { type: string; event?: AgentEvent; status?: SSEStatus };
    if (msg.type === "event" && msg.event) {
      eventCbs.forEach((cb) => cb(msg.event!));
    } else if (msg.type === "status" && msg.status) {
      statusStore.set(msg.status);
    }
  });

  transport.post({ type: "subscribe", sessionID: sessionId, base });

  return {
    onEvent(cb) { eventCbs.add(cb); },
    status: { subscribe: statusStore.subscribe },
    resync() {
      if (closed) return;
      // Fallback transport reopens/replays itself; the worker recovers when we
      // re-post subscribe (it refetches the snapshot or reconnects a dead ES).
      transport.resync?.();
      transport.post({ type: "subscribe", sessionID: sessionId, base });
    },
    close() {
      closed = true;
      eventCbs.clear();
      transport.post({ type: "unsubscribe", sessionID: sessionId });
      transport.close();
    },
  };
}
