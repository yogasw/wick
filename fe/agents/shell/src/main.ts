/*
 * agents-shell island — sidebar DOM enhancer.
 *
 * Attaches interactive behaviors to the AgentsLayout sidebar that is
 * server-rendered by layout.templ. Loaded by every agents page via the
 * <script type="module"> tag emitted by AgentsLayout.
 *
 * Behaviors ported from agents.js:
 *   - Pin project as personal default  (POST /projects/{id}/pin)
 *   - Drag session → project to move   (POST /sessions/{id}/project)
 *
 * The mobile drawer open/close and backdrop are already wired by an
 * inline <script> in layout.templ and are NOT duplicated here.
 */

import { statusDotClass, effectiveStatus, applyEvent, type DotState } from "./statusDot.js";

function resolveBase(): string {
  const el = document.querySelector<HTMLElement>("[data-base]");
  if (el?.dataset["base"]) {
    return el.dataset["base"];
  }
  const session = document.querySelector<HTMLElement>("[data-session-id]");
  if (session?.dataset["base"]) {
    return session.dataset["base"];
  }
  return "";
}

function wirePin(): void {
  document.querySelectorAll<HTMLButtonElement>("[data-pin-project]").forEach((btn) => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const id = btn.dataset["pinProject"];
      const base = resolveBase();
      if (!id || !base) {
        return;
      }
      btn.disabled = true;
      fetch(`${base}/projects/${encodeURIComponent(id)}/pin`, { method: "POST" })
        .then((r) => r.json())
        .then(() => {
          location.reload();
        })
        .catch(() => {
          btn.disabled = false;
        });
    });
  });
}

function wireDragToMove(): void {
  let dragSid: string | null = null;
  const dropHi = "ring-2 ring-green-400 ring-inset";
  const dropClasses = dropHi.split(" ");

  document.querySelectorAll<HTMLElement>("[data-session-drag]").forEach((row) => {
    row.addEventListener("dragstart", (e) => {
      dragSid = row.dataset["sessionDrag"] ?? null;
      if (e.dataTransfer) {
        e.dataTransfer.setData("text/plain", dragSid ?? "");
        e.dataTransfer.effectAllowed = "move";
      }
    });
    row.addEventListener("dragend", () => {
      dragSid = null;
    });
  });

  document.querySelectorAll<HTMLElement>("[data-project-drop]").forEach((target) => {
    target.addEventListener("dragover", (e) => {
      e.preventDefault();
      if (e.dataTransfer) {
        e.dataTransfer.dropEffect = "move";
      }
      dropClasses.forEach((c) => target.classList.add(c));
    });
    target.addEventListener("dragleave", () => {
      dropClasses.forEach((c) => target.classList.remove(c));
    });
    target.addEventListener("drop", (e) => {
      e.preventDefault();
      dropClasses.forEach((c) => target.classList.remove(c));
      const sid = (e.dataTransfer?.getData("text/plain")) || dragSid;
      const pid = target.dataset["projectDrop"];
      const base = resolveBase();
      if (!sid || !base) {
        return;
      }
      fetch(`${base}/sessions/${encodeURIComponent(sid)}/project`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project_id: pid }),
      })
        .then(() => {
          location.reload();
        })
        .catch(() => {});
    });
  });
}

/* Live sidebar liveness.

   Without this the spinners are a snapshot of whatever was running when
   the page was rendered: a session that starts working while you are
   reading another one never shows it, and one that finished keeps
   spinning until the next navigation.

   /stream/sessions carries lifecycle only, already filtered to the
   sessions this user may see. EventSource reconnects on its own, and the
   endpoint replays what is running on connect, so a dropped connection
   self-heals without any retry logic here. */
function wireLiveStatus(): void {
  const base = resolveBase();
  if (!base || typeof EventSource === "undefined") {
    return;
  }
  if (document.querySelectorAll("[data-session-dot]").length === 0) {
    return;
  }
  // Per-row state, because a row has two independent halves: its own
  // process and its sub-agents'. Collapsing them into the single string
  // the dot renders would mean a leader's "idle" event erased a running
  // sub-agent's marker — the exact moment that marker is the only thing
  // left worth showing.
  const rows = new Map<string, DotState>();
  const paint = (ev: { session_id?: string; lifecycle?: string; sub_agent?: string }) => {
    if (!ev.session_id) {
      return;
    }
    const next = applyEvent(rows.get(ev.session_id), ev);
    rows.set(ev.session_id, next);
    const dot = document.querySelector<HTMLElement>(
      `[data-session-dot="${CSS.escape(ev.session_id)}"]`,
    );
    // A session outside the sidebar's capped window has no node here.
    // Nothing to do — the row will render with the right state whenever
    // it does appear.
    if (dot) {
      dot.className = statusDotClass(effectiveStatus(next));
    }
  };

  // Route through the SharedWorker so every tab shares ONE /stream/sessions
  // connection.
  //
  // A browser allows only ~6 concurrent HTTP/1.1 connections per origin and an
  // SSE stream holds its slot for the life of the tab. One lifecycle stream per
  // tab, on top of the per-session stream and the dev-reload stream, exhausted
  // that quota: every request issued afterwards — an API call, an icon, a
  // navigation — sat unsent in the browser's own queue, which is
  // indistinguishable from a hung server. Closing a tab freed a slot and the
  // queued requests completed instantly; that is what pinned the cause here
  // rather than on the server.
  if (typeof SharedWorker !== "undefined") {
    try {
      const worker = new SharedWorker(
        new URL("@wick-fe/common-sse-worker/src/sse-worker.ts", import.meta.url),
        { type: "module" },
      );
      const port = worker.port;
      port.onmessage = (e: MessageEvent) => {
        const msg = e.data as { type?: string; event?: unknown } | null;
        if (!msg || msg.type !== "session") {
          return;
        }
        paint(msg.event as { session_id?: string; lifecycle?: string; sub_agent?: string });
      };
      port.start();
      port.postMessage({ type: "subscribe-lifecycle", base });
      window.addEventListener("pagehide", () => {
        port.postMessage({ type: "unsubscribe-lifecycle" });
        port.close();
      });
      return;
    } catch {
      // Fall through to the direct EventSource below.
    }
  }

  // Fallback for environments without SharedWorker (it costs a connection per
  // tab, which is what the worker path exists to avoid).
  const es = new EventSource(`${base}/stream/sessions`, { withCredentials: true });
  es.addEventListener("session", (e) => {
    let ev: { session_id?: string; lifecycle?: string; sub_agent?: string };
    try {
      ev = JSON.parse((e as MessageEvent).data as string);
    } catch {
      return;
    }
    paint(ev);
  });
  window.addEventListener("pagehide", () => es.close());
}

document.addEventListener("DOMContentLoaded", () => {
  wirePin();
  wireDragToMove();
  wireLiveStatus();
});
