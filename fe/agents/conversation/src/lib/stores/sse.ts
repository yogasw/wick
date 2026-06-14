/*
 * Purpose:    Session stream connector — wraps SharedWorker (or EventSource
 *             fallback) into a typed Svelte-store surface. Delivers AgentEvents
 *             via per-event callbacks rather than an accumulating array: callers
 *             react immediately without holding unbounded historical state.
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
  close(): void;
}

export interface SessionStream {
  /* Register a callback invoked for each incoming AgentEvent. */
  onEvent(cb: (e: AgentEvent) => void): void;
  status: Readable<SSEStatus>;
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

  /* Fallback: direct EventSource for environments without SharedWorker. */
  let handler: ((msg: unknown) => void) | null = null;
  const es = new EventSource(
    `${base}/stream?session=${encodeURIComponent(sessionID)}`,
    { withCredentials: true }
  );
  let subscribePosted = false;

  es.addEventListener("agent", (ev: MessageEvent) => {
    let parsed: AgentEvent;
    try { parsed = JSON.parse(ev.data as string) as AgentEvent; } catch (_) { return; }
    handler?.({ type: "event", sessionID, event: parsed });
  });

  es.onopen = () => {
    handler?.({ type: "status", sessionID, status: "connected" });
  };

  es.onerror = () => {
    handler?.({ type: "status", sessionID, status: "error" });
  };

  return {
    post(msg) {
      const m = msg as { type: string };
      if (m.type === "subscribe" && !subscribePosted) {
        subscribePosted = true;
      } else if (m.type === "unsubscribe") {
        es.close();
      }
    },
    onMessage(cb) { handler = cb; },
    close() { es.close(); },
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
    close() {
      closed = true;
      eventCbs.clear();
      transport.post({ type: "unsubscribe", sessionID: sessionId });
      transport.close();
    },
  };
}
