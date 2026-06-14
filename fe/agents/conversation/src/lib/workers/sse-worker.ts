/// <reference lib="webworker" />
/*
 * Purpose:    SharedWorker — holds one EventSource per sessionID, fans events
 *             to subscribed MessagePorts, fetches /stream/snapshot on each
 *             new subscribe. TypeScript port of internal/tools/agents/js/sse-worker.js.
 * Caller:     Instantiated by sse.ts via `new SharedWorker(new URL(...), { type: "module" })`
 * Dependencies: Browser EventSource, fetch
 * Main Functions: self.onconnect handler
 * Side Effects: Opens/closes EventSource connections per session
 */

/* Ports and EventSources keyed by sessionID. */
const ports: Record<string, Set<MessagePort>> = {};
const sources: Record<string, EventSource> = {};

function broadcast(sessionID: string, msg: unknown): void {
  const set = ports[sessionID];
  if (!set) return;
  set.forEach((p) => {
    try { p.postMessage(msg); } catch (_) { /* port gone */ }
  });
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

      if (sources[sid] && sources[sid].readyState !== EventSource.CLOSED) {
        port.postMessage({ type: "status", sessionID: sid, status: "connected" });
        fetch(`${base}/stream/snapshot?session=${encodeURIComponent(sid)}`, { credentials: "include" })
          .then((r) => r.json())
          .then((events: unknown[]) => {
            events.forEach((ev) => {
              port.postMessage({ type: "event", sessionID: sid, event: ev });
            });
          })
          .catch(() => {});
        return;
      }

      const url = `${base}/stream?session=${encodeURIComponent(sid)}`;
      const es = new EventSource(url, { withCredentials: true });
      sources[sid] = es;

      es.addEventListener("agent", (ev: MessageEvent) => {
        let parsed: unknown;
        try { parsed = JSON.parse(ev.data as string); } catch (_) { return; }
        broadcast(sid, { type: "event", sessionID: sid, event: parsed });
      });

      es.onopen = () => {
        broadcast(sid, { type: "status", sessionID: sid, status: "connected" });
      };

      es.onerror = () => {
        broadcast(sid, { type: "status", sessionID: sid, status: "error" });
      };

    } else if (data.type === "unsubscribe") {
      const sid = data.sessionID;
      if (sid === undefined || sid === null || !ports[sid]) return;
      ports[sid].delete(port);
      if (ports[sid].size === 0) {
        delete ports[sid];
        if (sources[sid]) {
          sources[sid].close();
          delete sources[sid];
        }
      }
    }
  };

  port.start();
};
