/*
 * Purpose:    The per-user /stream/sessions stream for the Team roster: each
 *             conversation's turn `activity` (thinking / the running tool /
 *             a failed tool / waiting on a person) plus connection status.
 * Caller:     AgentsApp.svelte
 * Dependencies: sse-worker.ts (SharedWorker), EventSource fallback
 * Main Functions: connectSessionsStream
 * Side Effects: Joins the SharedWorker's single /stream/sessions connection
 *
 * Through the SharedWorker the chat already uses, never an EventSource of its
 * own: an SSE stream holds one of the browser's ~6 connections per origin for
 * the life of the page (see sse-worker.ts), and every Team tab shares this one.
 */

/** One `activity` event: what a conversation's turn is doing. work "" =
    no turn running. */
export type SessionActivity = {
  session_id: string;
  work: "thinking" | "tool" | "";
  action: string;
  tool_error?: boolean;
  needs_attention?: boolean;
};

export type SessionsStreamHandlers = {
  onActivity: (ev: SessionActivity) => void;
  /** "connected" after every (re)open, "error" when the stream drops. */
  onStatus?: (status: "connected" | "error") => void;
};

/** Port is the slice of a MessagePort the stream uses; injectable for tests. */
export type SessionsPort = {
  postMessage(msg: unknown): void;
  onmessage: ((e: MessageEvent) => void) | null;
  start?(): void;
  close?(): void;
};

/** connectSessionsStream subscribes to the shared stream; the returned
    function leaves it. port overrides the SharedWorker (tests). */
export function connectSessionsStream(base: string, h: SessionsStreamHandlers, port?: SessionsPort): () => void {
  const p = port ?? workerPort();
  if (p) {
    p.onmessage = (e) => {
      const msg = e.data as { type?: string; event?: unknown; status?: string } | null;
      if (!msg) return;
      if (msg.type === "activity" && msg.event) h.onActivity(msg.event as SessionActivity);
      else if (msg.type === "lifecycle-status" && (msg.status === "connected" || msg.status === "error")) h.onStatus?.(msg.status);
    };
    p.start?.();
    p.postMessage({ type: "subscribe-lifecycle", base });
    const leave = () => {
      try { p.postMessage({ type: "unsubscribe-lifecycle" }); } catch { /* gone */ }
    };
    window.addEventListener("pagehide", leave);
    return () => {
      window.removeEventListener("pagehide", leave);
      leave();
      /* Same race as stores/sse.ts: let the unsubscribe flush first. */
      setTimeout(() => p.close?.(), 1000);
    };
  }

  /* No SharedWorker: one direct stream for this tab. */
  const es = new EventSource(`${base}/stream/sessions`, { withCredentials: true });
  es.addEventListener("activity", (e) => {
    try { h.onActivity(JSON.parse((e as MessageEvent).data as string) as SessionActivity); } catch { /* bad frame */ }
  });
  es.onopen = () => h.onStatus?.("connected");
  es.onerror = () => h.onStatus?.("error");
  return () => es.close();
}

function workerPort(): SessionsPort | null {
  if (typeof SharedWorker === "undefined") return null;
  try {
    const worker = new SharedWorker(
      new URL("@wick-fe/common-sse-worker/src/sse-worker.ts", import.meta.url),
      { type: "module" },
    );
    return worker.port;
  } catch {
    return null;
  }
}
