// Subscribe to the session's SSE stream via the shared SSE worker and
// invoke onSnapshot with the full git snapshot whenever a git_status
// event arrives. The payload IS the snapshot — the caller updates state
// directly, no follow-up fetch (zero polling). Returns a teardown fn.

import type { GitStatusSnapshot } from "$lib/api/scm";

const WORKER_URL = "/tools/agents/static/js/sse-worker.js";
const SUBSCRIBE_BASE = "/tools/agents";

export function subscribeGitStatus(
  sessionID: string,
  onSnapshot: (s: GitStatusSnapshot) => void,
): () => void {
  if (!sessionID) return () => {};
  let worker: SharedWorker | null = null;
  try {
    worker = new SharedWorker(WORKER_URL);
    worker.port.start();
  } catch {
    return () => {};
  }

  worker.port.onmessage = (msg: MessageEvent) => {
    const d = msg.data;
    if (!d || d.type !== "event") return;
    const ev = d.event;
    if (!ev || ev.type !== "git_status") return;
    try {
      onSnapshot(JSON.parse(ev.data) as GitStatusSnapshot);
    } catch {
      /* ignore malformed payload */
    }
  };

  worker.port.postMessage({ type: "subscribe", sessionID, base: SUBSCRIBE_BASE });

  return () => {
    if (worker) worker.port.postMessage({ type: "unsubscribe", sessionID });
  };
}
