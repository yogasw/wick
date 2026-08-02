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

/* statusDotClass mirrors view.StatusDotClass in layout.templ — the server
   renders the first paint, this re-renders the same node from live events.
   Change both together. */
function statusDotClass(lifecycle: string): string {
  const base = "ml-2 shrink-0 rounded-full";
  switch (lifecycle) {
    case "working":
      return `${base} h-3 w-3 border-2 border-green-500 border-t-transparent animate-spin`;
    case "spawning":
      return `${base} h-3 w-3 border-2 border-amber-400 border-t-transparent animate-spin`;
    case "queued":
      return `${base} h-1.5 w-1.5 bg-orange-500 animate-pulse`;
    case "idle":
      return `${base} h-1.5 w-1.5 bg-blue-400`;
    default:
      return `${base} hidden`;
  }
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
  const es = new EventSource(`${base}/stream/sessions`, { withCredentials: true });
  es.addEventListener("session", (e) => {
    let ev: { session_id?: string; lifecycle?: string };
    try {
      ev = JSON.parse((e as MessageEvent).data as string);
    } catch {
      return;
    }
    if (!ev.session_id) {
      return;
    }
    const dot = document.querySelector<HTMLElement>(
      `[data-session-dot="${CSS.escape(ev.session_id)}"]`,
    );
    // A session outside the sidebar's capped window has no node here.
    // Nothing to do — the row will render with the right state whenever
    // it does appear.
    if (dot) {
      // "killed" is the pool tearing a finished process down, not a
      // state worth a colour: it would leave a stale marker on a row
      // that is simply done.
      dot.className = statusDotClass(ev.lifecycle === "killed" ? "" : (ev.lifecycle ?? ""));
    }
  });
  window.addEventListener("pagehide", () => es.close());
}

document.addEventListener("DOMContentLoaded", () => {
  wirePin();
  wireDragToMove();
  wireLiveStatus();
});
