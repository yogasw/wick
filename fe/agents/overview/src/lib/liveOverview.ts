/*
 * Purpose:    Keeps the Overview fresh from the per-user /stream/sessions
 *             stream instead of a 3 s poll: its bare `pool` signal (a turn
 *             started/ended, a session queued) triggers one reload.
 * Caller:     App.svelte
 * Dependencies: sse-worker.ts (SharedWorker), EventSource fallback
 * Main Functions: followOverview
 * Side Effects: Joins the SharedWorker's /stream/sessions connection
 *
 * The signal carries nothing — the data stays /api/overview, which filters
 * per user — so this stream never shows a non-admin more than that endpoint
 * does. A slow poll covers only the time the stream is down, and a
 * reconnect reloads once because signals sent during the gap are gone.
 */

/** Port is the slice of a MessagePort used here; injectable for tests. */
export type OverviewPort = {
  postMessage(msg: unknown): void;
  onmessage: ((e: MessageEvent) => void) | null;
  start?(): void;
  close?(): void;
};

/** Fallback poll while the stream is down. */
export const OVERVIEW_FALLBACK_MS = 60_000;
/** A burst of transitions costs one reload. */
const COALESCE_MS = 300;

/** followOverview calls load now, then on every `pool` signal (coalesced),
    on reconnect, and on a slow timer only while the stream is down. The
    returned function stops all of it. */
export function followOverview(base: string, load: () => void, port?: OverviewPort | null): () => void {
  let up = false;
  let dropped = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const soon = () => {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => { timer = null; load(); }, COALESCE_MS);
  };
  const status = (s: string) => {
    up = s === "connected";
    if (!up) dropped = true;
    else if (dropped) { dropped = false; soon(); }
  };

  load();
  const poll = setInterval(() => { if (!up) load(); }, OVERVIEW_FALLBACK_MS);
  let leave: () => void = () => {};

  const p = port === undefined ? workerPort() : port;
  if (p) {
    p.onmessage = (e) => {
      const msg = e.data as { type?: string; status?: string } | null;
      if (!msg) return;
      if (msg.type === "pool") soon();
      else if (msg.type === "lifecycle-status" && msg.status) status(msg.status);
    };
    p.start?.();
    p.postMessage({ type: "subscribe-lifecycle", base });
    const bye = () => { try { p.postMessage({ type: "unsubscribe-lifecycle" }); } catch { /* gone */ } };
    window.addEventListener("pagehide", bye);
    leave = () => {
      window.removeEventListener("pagehide", bye);
      bye();
      setTimeout(() => p.close?.(), 1000);
    };
  } else if (typeof EventSource !== "undefined") {
    const es = new EventSource(`${base}/stream/sessions`, { withCredentials: true });
    es.addEventListener("pool", () => soon());
    es.onopen = () => status("connected");
    es.onerror = () => status("error");
    leave = () => es.close();
  }

  return () => {
    leave();
    if (timer !== null) clearTimeout(timer);
    clearInterval(poll);
  };
}

function workerPort(): OverviewPort | null {
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
